## 📝 Архитектура отказоустойчивого VPN (Nginx + Xray XHTTP + Hysteria 2)
## Общая схема работы
Сервер использует один внешний порт 443 для двух независимых протоколов, что обеспечивает максимальную скорость и маскировку:

   1. TCP (порт 443): Принимает nginx. Он показывает обычный сайт-маскировку для проверок DPI, а запросы к скрытому пути /api/v2/stream пересылает без шифрования внутрь сервера в xray (XHTTP) на порт 10080.
   2. UDP (порт 443): Принимает hysteria 2 напрямую. Работает параллельно с nginx, не конфликтуя с ним, и используется для максимальной скорости. [1] 

------------------------------
## 1. Конфигурация Nginx (/etc/nginx/sites-available/fi.kereal.qzz.io)
Веб-сервер работает как «щит» и обратный прокси. Настроен на обработку больших буферов в оперативной памяти (без сброса на диск) и удержание долгих соединений. [2] 

```
server {
    listen              443 ssl;
    listen              [::]:443 ssl;
    server_name         fi.kereal.qzz.io;
    http2               on;

    # Оптимизация таймаутов HTTP/2 для удержания сессии Xray
    keepalive_requests  100000;
    keepalive_timeout   3600s;
    client_header_timeout 3600s;

    # Оптимизация TLS для снижения нагрузки на CPU
    ssl_session_tickets on;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 24h;
    ssl_protocols       TLSv1.3;
    ssl_certificate     /etc/letsencrypt/live/fi.kereal.qzz.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/fi.kereal.qzz.io/privkey.pem;

    # Локация для проксирования Xray XHTTP
    location /api/v2/stream {
        access_log off; # Отключено, чтобы гигантские x_padding не забивали диск
        
        # Выделяем 1.5 МБ в ОЗУ под пакет, чтобы избежать варнингов буферизации на диск
        client_body_buffer_size 1500k; 
        
        proxy_redirect      off;
        proxy_pass          http://127.0.0.1:10080;
        proxy_http_version  1.1;
        
        proxy_set_header    Upgrade $http_upgrade;
        proxy_set_header    Connection "upgrade";
        proxy_set_header    Host $host;
        proxy_set_header    X-Real-IP $remote_addr;
        proxy_set_header    X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header    X-Forwarded-Proto $scheme;

        # Мгновенная отправка пакетов клиенту (минимальный пинг)
        proxy_buffering     off;
        proxy_cache         off;
        proxy_max_temp_file_size 0;
        
        proxy_read_timeout  300s;
        proxy_send_timeout  300s;
    }

    # Обычный сайт-маскировка
    location / {
        root /var/www/fi.kereal.qzz.io/html;
        index index.html index.htm;
        try_files $uri $uri/ =404;
    }
}
```

## 2. Параметры Xray Сервера (3x-ui / JSON инбаунда 10080)
Поскольку трафик идет через nginx внутри сервера, REALITY отключен, а безопасность выставлена в none. Используется стабильный режим packet-up с разогнанным мультиплексированием.

* Порт: 10080 (Прослушивание: 127.0.0.1)
* Протокол: VLESS (дешифрация: none, flow: пусто)
* Сеть (Network): xhttp
* Режим (Mode): packet-up (единственный потоковый режим, полностью совместимый со стандартным proxy_pass в nginx)
* Путь (Path): /api/v2/stream
* Макс. буферизованная загрузка (scMaxBufferedPosts): 90
* Макс. размер загрузки (scMaxEachPostBytes): 1350000 (оптимально под буфер nginx)
* Размещение сессии и номера пакета (sessionPlacement / seqPlacement): header
* Заголовок Server Max Header Bytes: 8192 (согласовано с лимитами nginx)
* Параметры xmux:
* maxConcurrency (Конкурентность): 128 (убирает задержки при загрузке множества элементов)
* hMaxRequestTimes: 3000-5000 (защита от микрофризов при частой смене сессий)
* hMaxReusableSecs (Время жизни сессии): 1800-3000 (30-50 минут, укладывается в таймауты nginx)
* cMaxReuseTimes: 2000

## ⚙️ Настройка функции «Внешний прокси» (External Proxy) в 3x-ui:
Чтобы панель при генерации подписки выдавала клиенту правильный конфиг, включен External Proxy со следующими параметрами:

* Force TLS: Включить (TLS)
* Внешний хост: fi.kereal.qzz.io
* Внешний порт: 443

------------------------------
## 3. Настройка клиента (Приложение HApp на устройстве)
Благодаря правильной настройке External Proxy, параметры прилетают через подписку автоматически. Профиль в happ содержит:

* Адрес и порт: fi.kereal.qzz.io : 443
* Безопасность (security): TLS (включен)
* SNI: fi.kereal.qzz.io (строго ваш домен!)
* ALPN: h2, http/1.1 (принудительный HTTP/2 для работы xmux)
* Fingerprint: chrome или safari (маскировка под обычный браузер)
* Mux / xmux: Включен (с теми же параметрами, что и на сервере)
* ECH config / SHA-256 сертификата: Оставить пустыми! (Let's Encrypt проверяется системой автоматически, а ECH сломает nginx)

------------------------------
## 4. Оптимизация ОС Linux (/etc/sysctl.conf)
Для снижения нагрузки на CPU под высокой сетевой нагрузкой применены оптимизации системных буферов и активирован быстрый алгоритм контроля TCP-очередей:
```
net.core.somaxconn = 10000
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
```

## 💡 Как пользоваться этой сборкой дальше:

* Режим «Веб-серфинг» (xray XHTTP): Используйте по умолчанию. Трафик выглядит как обычные переходы по сайту. Идеально обходит блокировки, не вызывает подозрений, работает в любых сетях, где открыт веб (TCP 443).
* Режим «Тяжелый трафик» (hysteria 2): Переключайтесь на нее, если нужно скачать большой файл, поиграть с минимальным пингом или посмотреть видео в 4K. Работает по UDP и игнорирует любые потери пакетов на плохом провайдере.

[1] [https://habr.com](https://habr.com/ru/articles/1008554/)
[2] [https://habr.com](https://habr.com/ru/companies/gnivc/articles/977196/)
