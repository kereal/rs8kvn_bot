## 📝 Архитектура отказоустойчивого VPN (Nginx + Xray XHTTP + Hysteria 2)
## Общая схема работы
Сервер использует один внешний порт 443 для двух независимых протоколов, что обеспечивает максимальную скорость и маскировку:

   1. TCP (порт 443): Принимает nginx. Он показывает обычный сайт-маскировку для проверок DPI, а запросы к скрытому пути /api/v2/stream пересылает без шифрования внутрь сервера в xray (XHTTP) на порт 10080.
   2. UDP (порт 443): Принимает hysteria 2 напрямую. Работает параллельно с nginx, не конфликтуя с ним, и используется для максимальной скорости. [1] 

------------------------------
## 1. Конфигурация Nginx (/etc/nginx/sites-available/fi.kereal.qzz.io)
Веб-сервер работает как «щит» и обратный прокси. Настроен на обработку больших буферов в оперативной памяти (без сброса на диск) и удержание долгих соединений. [2] 

```
upstream xray_xhttp {
    server 127.0.0.1:10080;
    keepalive 32; # Держим пул из 32 постоянно открытых соединений к Xray
}

server {

    listen              443 ssl;
    server_name         fi.kereal.qzz.io;
    http2               on;

    ssl_certificate     /etc/letsencrypt/live/fi.kereal.qzz.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/fi.kereal.qzz.io/privkey.pem;

    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 12h;
    ssl_session_tickets on;
    ssl_stapling on;
    ssl_stapling_verify on;

    access_log /var/log/nginx/fi.kereal.qzz.io.log;
    error_log  /var/log/nginx/fi.kereal.qzz.io.error.log warn;

    large_client_header_buffers 4 8k;
    client_header_buffer_size 1k;
    client_header_timeout 10s;
    proxy_headers_hash_max_size 1024;
    proxy_headers_hash_bucket_size 128;

    # Разрешаем бесконечное количество запросов внутри одного HTTP/2 соединения
    keepalive_requests 100000;

    # Увеличиваем время ожидания закрытия неактивного HTTP/2 соединения (по дефолту всего 3m)
    keepalive_timeout 3600s;

    # Заголовки безопасности (Делают сайт похожим на серьезный проект)
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options "nosniff" always;

    location /api/v2/stream {
        access_log off;
        proxy_redirect off;

        # Перенаправляем на Xray
        proxy_pass http://xray_xhttp;

        proxy_http_version 1.1;
        proxy_set_header Connection "";

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;

        proxy_buffering off;
        proxy_cache off;
        proxy_request_buffering off;

        # Защита от разрыва долгих соединений SplitHTTP провайдером или Nginx
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;

        client_max_body_size 0;
        client_body_buffer_size 1300k;
    }

    # Маскировка: корневая директория вашего реального сайта
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
* Путь (Path): /api/v2/stream/
* Макс. буферизованная загрузка (scMaxBufferedPosts): 90
* Макс. размер загрузки (scMaxEachPostBytes): 1350000 (оптимально под буфер nginx)
* Параметры xmux:
* maxConcurrency (Конкурентность): 128 (убирает задержки при загрузке множества элементов)
* hMaxRequestTimes: 3000-5000 (защита от микрофризов при частой смене сессий)
* hMaxReusableSecs (Время жизни сессии): 1800-3000 (30-50 минут, укладывается в таймауты nginx)
* cMaxReuseTimes: 2000

```
"streamSettings": {
    "network": "xhttp",
    "security": "none",
    "sockopt": {
      "tcpcongestion": "bbr",
      "trustedXForwardedFor": [
        "X-Real-IP"
      ]
    },
    "xhttpSettings": {
      "headers": {
        "Origin": "https://fi.kereal.qzz.io",
        "Referer": "https://fi.kereal.qzz.io/",
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
      },
      "host": "fi.kereal.qzz.io",
      "mode": "packet-up",
      "path": "/api/v2/stream/",
      "scMaxBufferedPosts": 90,
      "scMaxEachPostBytes": "1200000",
      "xPaddingBytes": "128-1024"
    }
}
```

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
