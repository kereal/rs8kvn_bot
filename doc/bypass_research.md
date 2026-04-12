# Исследование методов обхода блокировок — Консолидированный справочник

> **Технический документ для проекта rs8kvn_bot**
> Версия: 3.0 (Консолидированная)
> Дата: Март 2026
> Целевая аудитория: разработчики, системные администраторы, пользователи VPN

---

## Содержание

1. [Архитектура цензуры](#1-архитектура-цензуры)
2. [Принципы обхода DPI](#2-принципы-обхода-dpi)
3. [Техники маскировки трафика](#3-техники-маскировки-трафика)
4. [Протоколы и их характеристики](#4-протоколы-и-их-характеристики)
5. [Основные ядра и ПО](#5-основные-ядра-и-по)
6. [Новые и малоизвестные инструменты 2024-2025](#6-новые-и-малоизвестные-инструменты-2024-2025)
7. [Архитектурные паттерны](#7-архитектурные-паттерны)
8. [Продвинутые техники](#8-продвинутые-техники)
9. [Сравнительные таблицы](#9-сравнительные-таблицы)
10. [Мониторинг и детектирование блокировок](#10-мониторинг-и-детектирование-блокировок)
11. [Антипаттерны](#11-антипаттерны)
12. [Рекомендации для rs8kvn_bot](#12-рекомендации-для-rs8kvn_bot)
13. [Ссылки и ресурсы](#13-ссылки-и-ресурсы)

---

## 1. Архитектура цензуры

### 1.1 DPI (Deep Packet Inspection)

DPI-системы анализируют пакеты на всех уровнях модели OSI. В России используется оборудование TSPU (Технические средства противодействия угрозам), установленное на магистральных каналах провайдеров.

**Методы детекции:**

| Метод | Принцип | Контрмера |
|-------|---------|-----------|
| SNI Inspection | Чтение домена в TLS ClientHello | ECH, ESNI, Reality |
| TLS Fingerprinting | Анализ сигнатур handshake | Имитация легитимного клиента |
| Protocol Detection | Поиск паттернов в payload | Обфускация, маскировка |
| Statistical Analysis | Размеры, тайминги, частота | Рандомизация, padding |
| Flow Analysis | Корреляция сессий | Multi-hop, proxy chains |

**Ключевые fingerprint-параметры TLS:**
- Набор cipher suites и их порядок
- Расширения TLS и их последовательность
- Размеры записей TLS record layer
- Паттерны фрагментации

### 1.2 Методы блокировки

**IP-based:**
- BGP blackholing
- ACL на маршрутизаторах
- Блокировка подсетей (/24, /16)

**DNS-based:**
- Подмена DNS-ответов
- Перехват DNS-запросов (UDP/TCP 53)
- Блокировка DoH/DoT серверов

**Protocol-based:**
- Детекция по сигнатурам (WireGuard: фиксированный размер 148 байт, предсказуемые заголовки)
- Анализ timing-паттернов
- Глубокий анализ handshake протоколов

**Throttling:**
- Token bucket policer
- Пакетная потеря (random drop)
- Latency injection

### 1.3 Эволюция блокировок в РФ

- **2018:** IP-блокировки (Telegram, побочный ущерб)
- **2019:** Закон о "суверенном интернете"
- **2021:** Throttling (Twitter/X)
- **2022:** Масштабные IP/DPI блокировки
- **2024:** Тотальное замедление YouTube, блокировки VPN-протоколов

**Текущий ландшафт:**
- Активное DPI на всех магистралях
- Блокировка известных VPN-IP по постановлениям
- Протокол-ориентированные блокировки (WireGuard, OpenVPN)
- Тестирование whitelist-режима

---

## 2. Принципы обхода DPI

### 2.1 Фундаментальные подходы

**1. Маскировка под легитимный трафик**

Цель: сделать туннель неотличимым от обычного HTTPS.

```
Реальный HTTPS:
  TLS handshake → HTTP/2 или HTTP/3 → Encrypted data

Туннель с маскировкой:
  TLS handshake (имитация браузера) → Туннельные данные (внутри TLS)
```

**2. Использование разрешённых протоколов**

- HTTP/2, HTTP/3 (QUIC) — разрешены везде
- WebSocket — стандарт для real-time приложений
- gRPC — распространён в микросервисах

**3. Размытие fingerprint'а**

- Рандомизация размеров пакетов (padding)
- Имитация паттернов легитимного приложения
- Использование стандартных cipher suites

### 2.2 Уровни обфускации

```
Уровень 0: Без обфускации
  Протокол → DPI видит всё → БЛОКИРОВКА

Уровень 1: TLS wrapping
  Протокол → TLS → DPI видит SNI, TLS fingerprint

Уровень 2: TLS + Transport obfuscation
  Протокол → TLS (custom fingerprint) → WS/gRPC → DPI видит HTTPS

Уровень 3: TLS + Reality/ECH
  Протокол → TLS (легитимный fingerprint) → DPI не отличает от реального HTTPS

Уровень 4: Мульти-уровневая маршрутизация
  Протокол → TLS → Proxy chain → Multiple exit nodes
```

### 2.3 Ключевые принципы

**Неотличимость (Indistinguishability):**

Трафик туннеля должен быть статистически неотличим от легитимного:
- TLS fingerprint: совпадает с Chrome/Firefox signature
- Packet sizes: variable, MTU-aligned
- Timing: human-like patterns

**Отказоустойчивость (Resilience):**
- Мульти-серверная архитектура
- Автоматический failover
- Несколько протоколов в арсенале

**Минимальная энтропия:**

Избегать уникальных паттернов:
- Не использовать специфичные cipher suites
- Не отправлять данные сразу после handshake
- Имитировать keep-alive паттерны

---

## 3. Техники маскировки трафика

### 3.1 TLS-маскировка

#### SNI Spoofing

**Устаревший метод — Domain Fronting:**
```
Client → CDN Edge:
  SNI: allowed-domain.com (видит DPI)
  Host: blocked-domain.com (внутри TLS)

CDN маршрутизировал по Host header.
Статус: ❌ Заблокирован большинством CDN (2018-2020).
```

**Современный метод — Encrypted Client Hello (ECH):**

```
Стандартный TLS 1.3:
  ClientHello → SNI: blocked.com (открытый текст) → DPI видит

ECH (RFC 9460):
  ClientHelloOuter:
    SNI: public.com (видит DPI)
  ClientHelloInner (encrypted):
    SNI: blocked.com (реальный домен)
```

**Требования ECH:**
- DNS HTTPS RR с ECH config
- Клиент с поддержкой ECH (Chrome 117+, Firefox 118+)
- Сервер с ECH (Caddy, Cloudflare)

**Ограничения:**
- Требует DoH для получения ECH config
- DNS-блокировка предотвращает работу
- Не все серверы поддерживают

#### Reality

**Принцип:** Использование чужого TLS-сертификата для handshake.

```
Клиент → Сервер Reality:
  1. ClientHello с SNI = target-site.com (реальный сайт)
  2. Server отправляет сертификат target-site.com
  3. Клиент проверяет сертификат (но не валидирует CA)
  4. Устанавливается туннель внутри "чужой" TLS-сессии

DPI видит:
  - Легитимное TLS-рукопожатие с target-site.com
  - Сертификат настоящего сайта
  - Невозможно отличить от реального подключения
```

**Ключевое отличие от Domain Fronting:**
- Не требует CDN
- Не зависит от третьих сторон
- Работает с любым HTTPS-сайтом

**Target sites (примеры):** `www.microsoft.com`, `www.apple.com`, `www.amazon.com` — любой крупный HTTPS-сайт с TLS 1.3

### 3.2 Transport обфускация

#### WebSocket Transport

```
HTTP Upgrade Request:
  GET /path HTTP/1.1
  Host: example.com
  Upgrade: websocket
  Connection: Upgrade

После upgrade: Binary WebSocket frames → туннельные данные
```

Преимущества: выглядит как обычный WebSocket, работает через CDN, совместим с HTTP/2, HTTP/3.

#### HTTP/2 Transport

Один TCP-соединение → Multiple HTTP/2 streams: туннельные потоки + masquerade traffic. Нативный header compression (HPACK).

#### gRPC Transport

Имитация микросервисного трафика: `POST /ServiceName/MethodName`, Content-Type: `application/grpc`. Multi-mode для параллельных потоков, поддержка CDN.

#### QUIC/HTTP3 Transport

UDP-based протокол. UDP трудно блокировать без коллатерального ущерба. Встроенная мультиплексировка, быстрое восстановление соединения (0-RTT). Проблемы: QUIC fingerprint может детектироваться, UDP throttling.

### 3.3 Протокольная обфускация

#### WireGuard Obfuscation

**Проблема WireGuard:**
```
WireGuard Initiation Packet (IPv4):
  Type: 0x01 | Reserved | Sender index | Ephemeral key | Encrypted static key | Encrypted timestamp | MAC
  Total: 148 байт (фиксированный!)

DPI детектирует: фиксированный размер 148 байт, предсказуемые байты, timing паттерны
```

**Решение — AmneziaWG** (модифицированный WireGuard):

Добавляет "junk packets" для размытия fingerprint:
- `Jc` = Junk packet count (4-10)
- `Jmin/Jmax` = Junk packet size range (40-100 bytes)
- `S1, S2` = Sequence parameters
- `H1-H4` = Magic headers (модифицированные заголовки)

Результат: размер пакетов варьируется, timing рандомизирован, заголовки модифицированы.

#### Shadowsocks AEAD

Symmetric encryption с authenticated encryption (AES-128-GCM, AES-256-GCM, ChaCha20-Poly1305). Каждый пакет содержит Salt (random, 16 bytes) + Encrypted payload + Authentication tag.

Плагины обфускации: v2ray-plugin (WebSocket + TLS, ✅ работает), obfs-local (⚠️ устарел), kcptun (⚠️ детектируется).

### 3.4 Traffic Shaping

**Padding:**
```
Original packet: 64 bytes
Padded packet: 128 bytes (aligned to common size)
```

**Timing randomization:**
```
Normal VPN: Send → Immediate response → Detectable
Shaped traffic: Send → Random delay (10-200ms) → Response
```

**Burst simulation:** Имитация HTTP burst pattern — 3-5 пакетов rapidly, пауза 100-500ms, повтор.

---

## 4. Протоколы и их характеристики

### 4.1 Сравнительная таблица протоколов

| Протокол | Транспорт | Fingerprint | Устойчивость в РФ | Использование |
|----------|-----------|-------------|-------------------|---------------|
| **VLESS+Reality** | TCP/WS/gRPC | Невидим (маскируется) | ⭐⭐⭐⭐⭐ | Основной выбор |
| **TrustTunnel** | HTTP/2, HTTP/3 | HTTP/2, QUIC | ⭐⭐⭐⭐⭐ | Максимальная скрытность |
| **Hysteria2** | QUIC | QUIC-подобный | ⭐⭐⭐⭐ | Высокая скорость |
| **AmneziaWG** | UDP | Обфусцированный WG | ⭐⭐⭐⭐ | WireGuard-совместимый |
| **Trojan** | TLS | Стандартный TLS | ⭐⭐⭐ | Требует сертификат |
| **Shadowsocks** | TCP/UDP | Зависит от плагина | ⭐⭐-⭐⭐⭐ | Требует обфускацию |
| **WireGuard** | UDP | Детектируется | ⭐ | ❌ Блокируется |
| **OpenVPN** | TCP/UDP | Детектируется | ⭐ | ❌ Блокируется |

### 4.2 Детальный анализ протоколов

#### VLESS (V2Ray/Xray)

- UUID authentication (вместо пароля)
- Flow control (xtls-rprx-vision)
- Минимальный overhead
- Отсутствие fingerprint на уровне протокола
- Поддержка всех transport типов (WS, gRPC, H2, Reality)
- Reality integration: VLESS payload → Reality TLS session → Невидим для DPI

#### VMess (V2Ray Legacy)

- Встроенное шифрование (AEAD)
- Time-based authentication
- Большой overhead
- **Статус:** Устарел, VLESS — современная замена

#### Trojan

- Password → Authenticates client, внутри TLS → прозрачный TCP proxy
- Требует реальный TLS-сертификат и домен (SNI виден DPI)
- Trojan-Go variant: Trojan → Reality transport → Невидим

#### Hysteria2

- QUIC-based (HTTP/3 transport layer)
- Brutal congestion control для агрессивной пропускной способности
- Masquerade режим (выглядит как HTTP/3)
- SAL (Salamander) obfuscation
- 0-RTT connection resumption
- Multiplexing streams

#### TrustTunnel

- Туннелирование через HTTP/2 или HTTP/3
- TCP: HTTP CONNECT method → TCP stream
- UDP: HTTP/2 streams → UDP multiplexing (`_udp2` pseudo-host)
- ICMP: HTTP encapsulation → ICMP packets
- Встроенный health check (`_check` endpoint)
- Support для multiple addresses (load balancing)
- **Стелс-уровень:** Максимальный — DPI видит только HTTP traffic

#### AmneziaWG

- WireGuard + обфускация (Jc, Jmin/Jmax, S1/S2, H1-H4)
- Размер пакетов варьируется, timing рандомизирован, заголовки модифицированы
- Сохраняется производительность WireGuard
- Накладные расходы: ~10-20% на обфускацию

#### Leaf (Rust proxy framework)

- Inbound: HTTP, SOCKS5, Shadowsocks, Trojan
- Outbound: SOCKS5, Shadowsocks, Trojan, VMess, VLESS, Reality
- Transports: WebSocket, TLS, QUIC, AMux, Reality, MPTP
- Уникальная особенность — MPTP (Multi-path Transport Protocol): агрегация нескольких каналов для увеличения throughput и failover

#### Shadowsocks 2022

- Улучшенная версия с усиленной криптографией (2022-blake3-aes-128-gcm)
- Может детектироваться DPI, требует плагины для обфускации

#### TUIC

- QUIC-based прокси протокол
- Оптимизирован для TCP/UDP, поддержка congestion control (BBR)
- Меньше клиентов по сравнению с Hysteria2

---

## 5. Основные ядра и ПО

### 5.1 Обзор современных ядер

**Ядро VPN/прокси** — программный компонент, реализующий логику работы протоколов туннелирования: устанавливает соединения, шифрует/дешифрует, управляет маршрутизацией, обеспечивает мультиплексирование.

| Ядро | Язык | Протоколы | Рейтинг | Статус |
|------|------|-----------|---------|--------|
| **Xray-core** | Go | VLESS, VMess, Trojan, Shadowsocks | ⭐⭐⭐⭐⭐ | Активное развитие |
| **Mihomo** | Go | VLESS, VMess, Trojan, Hysteria2, TUIC | ⭐⭐⭐⭐⭐ | Активное развитие |
| **Sing-box** | Go | VLESS, VMess, Trojan, Hysteria2, TUIC | ⭐⭐⭐⭐⭐ | Активное развитие |
| **AmneziaWG** | Go/C | WireGuard + обфускация | ⭐⭐⭐⭐ | Активное развитие |
| **TrustTunnel** | Rust | HTTP/2, HTTP/3 (QUIC) | ⭐⭐⭐⭐ | Новое, активное |
| **Leaf** | Rust | VLESS, VMess, Trojan, Shadowsocks | ⭐⭐⭐⭐ | Активное развитие |
| **Cloudflare Warp** | Go/Rust | WireGuard, MASQUE | ⭐⭐⭐⭐ | Стабильное |

### 5.2 Xray-core

**История:** Форк V2Ray с 2020 года. Ключевые события: 2021 — Reality протокол, 2022 — XTLS Vision flow.

**Уникальные технологии:**

- **Reality** — маскировка без сертификата: клиент отправляет ClientHello с SNI легитимного сайта, сервер отвечает сертификатом этого сайта, DPI видит обычный HTTPS
- **XTLS** — оптимизация обработки TLS трафика
- **Vision flow** (`xtls-rprx-vision`) — оптимизированный режим для VLESS

**Базовая конфигурация VLESS+Reality (сервер):**

```json
{
  "inbounds": [{
    "tag": "vless-reality",
    "protocol": "vless",
    "listen": "0.0.0.0",
    "port": 443,
    "settings": {
      "clients": [{"id": "uuid-here", "flow": "xtls-rprx-vision"}],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "dest": "www.microsoft.com:443",
        "serverNames": ["www.microsoft.com"],
        "privateKey": "private-key-here",
        "shortIds": ["short-id-1"]
      }
    }
  }],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}
```

**Мульти-серверность:** Routing rules, Balancer (leastPing), Observatory (мониторинг серверов).

**Для кого подходит:** Продвинутые пользователи, сисадмины, проекты с максимальной скрытностью (Reality). Новичкам лучше панели типа 3x-ui.

### 5.3 Mihomo (Clash Meta)

**История:** Форк оригинального Clash (2021), добавление VLESS, Reality, Hysteria2. Новый брендинг Mihomo с 2023.

**Поддерживаемые протоколы:** VLESS ✅, VMess ✅, Trojan ✅, Shadowsocks ✅, Hysteria2 ✅, TUIC ✅, WireGuard ✅ (ограниченно).

**Уникальные возможности:**

**Proxy Groups** — группы прокси с разными стратегиями:

```yaml
proxy-groups:
  - name: "Auto"
    type: url-test
    proxies: [NL-Server, DE-Server, FI-Server]
    url: "http://www.gstatic.com/generate_204"
    interval: 300
    tolerance: 50

  - name: "Fallback"
    type: fallback
    proxies: [NL-Server, DE-Server, DIRECT]

  - name: "Proxy"
    type: select
    proxies: [Auto, Fallback, NL-Server, DE-Server, DIRECT]
```

**Rule-based routing:**

```yaml
rules:
  - DOMAIN-SUFFIX,telegram.org,Proxy
  - DOMAIN-SUFFIX,youtube.com,Proxy
  - DOMAIN-SUFFIX,vk.com,DIRECT
  - DOMAIN-SUFFIX,yandex.ru,DIRECT
  - GEOIP,RU,DIRECT
  - MATCH,Proxy
```

**Для кого подходит:** Пользователи с несколькими серверами, любители rule-based routing. Не подходит для простой конфигурации.

### 5.4 Sing-box

**История:** Современная прокси-платформа на Go с 2022 года. Фокус на производительность и чистоту кода.

**Поддерживаемые протоколы:** VLESS ✅, VMess ✅, Trojan ✅, Shadowsocks ✅, Hysteria2 ✅, TUIC ✅, WireGuard ✅.

**Уникальные возможности:**
- Современная модульная архитектура
- Эффективное использование памяти
- JSON конфигурация

**Пример конфигурации:**

```json
{
  "inbounds": [
    {"type": "tun", "tag": "tun-in", "inet4_address": "172.19.0.1/30", "auto_route": true},
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 7890}
  ],
  "outbounds": [
    {
      "type": "vless", "tag": "proxy-nl", "server": "nl.example.com", "server_port": 443,
      "uuid": "uuid-here", "flow": "xtls-rprx-vision",
      "tls": {
        "enabled": true, "server_name": "www.microsoft.com",
        "utls": {"enabled": true, "fingerprint": "chrome"},
        "reality": {"enabled": true, "public_key": "...", "short_id": "..."}
      }
    },
    {"type": "urltest", "tag": "auto", "outbounds": ["proxy-nl", "proxy-de"], "interval": "3m"},
    {"type": "selector", "tag": "proxy", "outbounds": ["auto", "proxy-nl", "proxy-de"]}
  ],
  "route": {"final": "proxy", "auto_detect_interface": true}
}
```

**Мульти-серверность:** URLTest (автовыбор), Selector (ручной выбор), LoadBalance (распределение нагрузки).

**Для кого подходит:** Разработчики, любители современной архитектуры. Меньше GUI клиентов, чем у Clash.

### 5.5 Cloudflare Warp

**История:** VPN-сервис от Cloudflare (2019). Два протокола:

| Протокол | Описание | Эффективность |
|----------|----------|---------------|
| **WireGuard** | Классический WireGuard | ⚠️ Блокируется DPI по fingerprint |
| **MASQUE** | HTTP/3 с TLS 1.3 (FIPS 140-2) | ✅ Выглядит как обычный HTTP/3 |

**Два режима:** 1.1.1.1 (только DNS) и WARP (полный VPN). Per-app VPN поддержка. 310+ дата-центров.

**Настройка протокола:**
```bash
warp-cli register
warp-cli tunnel protocol set MASQUE  # или WireGuard
warp-cli connect
```

**В России (2026):** ⚠️ Частично работает, но часто блокируется. WireGuard режим блокируется DPI. MASQUE может блокироваться по IP Cloudflare.

**Для кого подходит:** Обычные пользователи за пределами России. В России не рекомендуется.

### 5.6 AmneziaWG

**История:** Модифицированный WireGuard от AmneziaVPN (2022). Специально для обхода DPI-блокировок.

**Технология обфускации:**

```
Обычный WireGuard: фиксированный 148 байт, 0x01 заголовок → Detected!
AmneziaWG: Junk packets + Magic Headers + Random padding → Cannot detect!
```

**Параметры обфускации:**

| Параметр | Описание | Пример |
|----------|----------|--------|
| Jc | Junk Packet Count | 4 |
| Jmin/Jmax | Junk Packet Size Range | 40/70 |
| S1/S2 | Sequence Parameters | 20/20 |
| H1-H4 | Magic Headers | 0x12345678... |

**Пример конфигурации:**

```ini
[Interface]
PrivateKey = your-private-key
Address = 10.8.0.2/24
DNS = 1.1.1.1
Jc = 4
Jmin = 40
Jmax = 70
S1 = 20
S2 = 20
H1 = 0x12345678
H2 = 0x87654321
H3 = 0xabcdef00
H4 = 0x00fedcba

[Peer]
PublicKey = peer-public-key
Endpoint = 203.0.113.1:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

**Сравнение с оригинальным WireGuard:**

| Характеристика | WireGuard | AmneziaWG |
|----------------|-----------|-----------|
| Скорость | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ (немного медленнее) |
| Скрытность | ❌ Легко детектируется | ✅ Обфусцирован |
| Обход DPI в РФ | ❌ Блокируется | ✅ Работает |
| Накладные расходы | Минимальные | ~10-20% на обфускацию |

**Поддерживаемые платформы:** Linux ✅, Windows ✅, macOS ✅, Android ✅, iOS ✅, Keenetic ✅ (beta).

**Интеграция с AmneziaVPN:** Автоматическая настройка, простой GUI для всех платформ, интеграция с Keenetic роутерами, поддержка других протоколов (OpenVPN over Cloak, Shadowsocks, XRay).

**В России (2026):** ✅ Работает хорошо. Рекомендуется для обычных пользователей через AmneziaVPN. Для максимальной скрытности лучше VLESS+Reality.

### 5.7 TrustTunnel

**История:** Протокол от AdGuard VPN, open-source с 2024 года.

**Архитектура:** Маскирует весь трафик под обычный HTTP/2 или HTTP/3 (QUIC).

**Поддерживаемые транспорта:**

| Транспорт | Протокол | Эффективность |
|-----------|----------|---------------|
| HTTP/1.1 | HTTP/1.1 over TLS | ⭐⭐⭐ |
| HTTP/2 | HTTP/2 over TLS | ⭐⭐⭐⭐⭐ |
| HTTP/3 | HTTP/3 over QUIC | ⭐⭐⭐⭐⭐ |

**Туннелирование трафика:**
- **TCP** — через HTTP CONNECT
- **UDP** — через мультиплексированный stream (`_udp2` pseudo-host)
- **ICMP** — через HTTP encapsulation

**Серверная конфигурация (vpn.toml):**

```toml
listen_address = "0.0.0.0:443"
credentials_file = "credentials.toml"

[listen_protocols.http2]
initial_connection_window_size = 8388608
initial_stream_window_size = 131072
max_concurrent_streams = 1000

[listen_protocols.quic]
recv_udp_payload_size = 1350
enable_early_data = true
```

**Мульти-серверность:** Несколько адресов для балансировки, health checks, automatic failover.

**В России (2026):** ✅ Работает отлично. Рекомендуется как резервный протокол к VLESS+Reality.

**Для кого подходит:** Продвинутые пользователи, проекты с мульти-серверной архитектурой. Не для новичков.

### 5.8 Leaf

**История:** Универсальный прокси-фреймворк на Rust (2021).

**Архитектура:** Модульная — Inbounds, Outbounds, Transports, Traffic Control (Chain, Failover), Transparent Proxying (TUN, NF).

**Поддерживаемые протоколы:** HTTP (in), SOCKS5, Shadowsocks, Trojan, VMess (out), VLESS (out, Reality).

**Поддерживаемые транспорты:** WebSocket, TLS, QUIC, AMux, Obfs, Reality, MPTP.

**MPTP — Multi-path Transport Protocol:**

```
Client → MPTP → Server
          ├─ Path 1: WiFi
          ├─ Path 2: 4G
          └─ Path 3: Ethernet

Aggregation → Увеличение throughput
Failover → Автоматическое переключение между paths
```

**Для кого подходит:** Разработчики, продвинутые пользователи, канальная агрегация. Не подходит для новичков и тех, кому нужен GUI.

---

## 6. Новые и малоизвестные инструменты 2024-2025

### 6.1 mieru (Go)

**Репозиторий:** `enfein/mieru` | ⭐1625 | GPL-3.0

**Тип:** SOCKS5 / HTTP / HTTPS прокси

**Уникальные особенности:**
- Не использует TLS протокол — не требует домен или сертификат
- Шифрование XChaCha20-Poly1305 с генерацией ключей на основе username, password и system time
- Random padding и replay attack detection
- Поддержка множества пользователей на одном сервере

**Архитектура:**
```
Client (mieru) ←→ [Encrypted Channel] ←→ Server (mita) ←→ Internet
                  No TLS handshake, No domain required
```

**В России:** ⭐⭐⭐⭐ (высокая) — DPI не распознаёт как VPN/прокси, нет характерного TLS fingerprint.

### 6.2 fraud-bridge (C++)

**Репозиторий:** `stealth/fraud-bridge` | ⭐228

**Тип:** ICMP / DNS / NTP туннелирование

**Архитектура:**
```
[Inside host] ←ICMP/DNS/NTP→ [Outside server] → Internet
```

**ICMP туннелирование:** Overhead всего 24 байта (HMAC-MD5 integrity).
**DNS туннелирование:** До 1232 байт на пакет, EDNS0.
**NTP туннелирование:** UDP-based, работает через CGN.

**Уникальные возможности:** MSS clamping, roaming support (SSH sessions выживают при смене IP), chroot support.

**В России:** ⭐⭐⭐ (средняя-высокая) — использовать когда TCP/UDP заблокированы. ICMP: приоритетный выбор.

### 6.3 nooshdaroo (Rust)

**Репозиторий:** `RostamVPN/nooshdaroo` | ⭐18 | MIT | Март 2026

**Тип:** DNS-туннелированный SOCKS5 прокси

**Размер бинарника:** 982KB, нет внешних зависимостей.

**Протокольный стек:**
```
SOCKS5 → smux v2 (stream multiplexing) → Noise_NK (authenticated encryption)
→ KCP (reliable transport) → DNS queries (base32 in QNAME, TXT RDATA)
```

**Криптография:** `Noise_NK_25519_ChaChaPoly_BLAKE2s`

**Chrome DNS fingerprinting:** AD=1 flag, EDNS0 UDP size: 1452, A+AAAA+HTTPS query pairs, burst timing.

**DNS Flux:** Time-based multi-domain selection (6-hour periods). OTA config updates через DNS TXT records.

**В России:** ⭐⭐⭐⭐ (высокая) — крайне скрытный, для экстремальных сценариев.

### 6.4 prisma (Rust)

**Репозиторий:** `Yamimega/prisma` | ⭐0 | GPL-3.0 | Март 2026

**Тип:** Encrypted proxy infrastructure suite

**Уникальная технология:** PrismaVeil v5 wire protocol
- 1-RTT handshake, 0-RTT resumption
- X25519 + BLAKE3 + ChaCha20/AES-256-GCM
- Connection migration, Enhanced KDF

**8 транспортов:** QUIC v2, PrismaTLS (Reality alternative), WebSocket, gRPC, XHTTP, XPorta, SSH, WireGuard.

**PrismaTLS:** Browser fingerprint mimicry, Dynamic mask server pool, Active probing resistance.

**Traffic Shaping:** Bucket padding, Timing jitter, Chaff injection, Frame coalescing.

**Native GUI клиенты:** Windows (Win32/GDI), Android (Jetpack Compose), iOS (SwiftUI), macOS.

**В России:** ⭐⭐⭐⭐ (потенциально высокая) — Monitor development, test in staging.

### 6.5 burrow (Go)

**Репозиторий:** `FrankFMY/burrow` | ⭐2 | Apache-2.0 | Март 2026

**Тип:** Self-hosted VPN для censorship circumvention

**Принцип:** Deploy in one command, connect in one click.

**Поддерживаемые протоколы:**

| Протокол | Порт | Описание |
|----------|------|----------|
| VLESS+Reality | 443/TCP | Primary, маскировка под HTTPS |
| VLESS+WebSocket (CDN) | 8080/TCP | Cloudflare-fronted |
| Hysteria2 | 8443/UDP | QUIC-based |
| Shadowsocks 2022 | 8388/TCP | Modern encryption |

**Уникальные возможности:**
- Desktop Client: System proxy, Speed test, QR import
- Server: Auto TLS (ACME/Let's Encrypt), Multi-user, Traffic stats, gRPC API
- API Endpoints: `/api/v1/servers`, `/api/v1/users`, `/api/v1/traffic`

**В России:** ⭐⭐⭐⭐⭐ (очень высокая) — VLESS+Reality первичный, Hysteria2 резервный.

### 6.6 geph4-client (Rust)

**Репозиторий:** `geph-official/geph4` | ⭐2.4k

**Тип:** Anti-censorship VPN

**Уникальный подход:** Не один протокол, а стратегия "много слоёв":
- Level 1: obfs4 (Tor-like, не блокируется)
- Level 2: VLESS+Reality (быстрый, скрытный)
- Level 3: Custom SOS (если всё остальное заблокировано)

**В России:** ⭐⭐⭐⭐ (высокая) — специально разработан для Китая/Ирана/России. Бесплатный тариф ограничен.

### 6.7 snowflake (Go)

**Репозиторий:** `PluggableTransports/snowflake` | Часть Tor Project

**Тип:** WebRTC-based proxy (Tor pluggable transport)

**Как работает:**
1. Волонтёры запускают Snowflake proxy в браузере (WebRTC)
2. Пользователь подключается к случайному волонтёру
3. Трафик выглядит как WebRTC video call
4. Сотни/тысячи прокси — невозможно заблокировать всех

**В России:** ⭐⭐⭐ (средняя) — работает, но медленнее VPN. Подходит для базового доступа, не для стриминга.

### 6.8 yggdrasil-go (Go)

**Репозиторий:** `yggdrasil-network/yggdrasil-go` | ⭐3.1k

**Тип:** Decentralized overlay network (не VPN!)

**Аритектура:** Mesh-сеть с IPv6 адресами на основе криптографических ключей. Нет центральных серверов — каждый узел равноправен.

**Use cases:** Децентрализованный доступ без точек отказа, peer-to-peer между серверами, резервный канал при блокировке всех VPN.

**В России:** ⭐⭐⭐ (средняя) — не блокируется (нет centralised fingerprint), но не даёт выхода в обычный интернет напрямую.

### Сравнительная таблица новых инструментов

| Инструмент | Язык | Протокол | Скрытность | Скорость | Для РФ |
|------------|------|----------|-----------|----------|--------|
| **mieru** | Go | XChaCha20, no TLS | ⭐⭐⭐⭐ | ⭐⭐⭐ | ✅ |
| **fraud-bridge** | C++ | ICMP/DNS/NTP | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⚠️ Экстрим |
| **nooshdaroo** | Rust | DNS+Noise+KCP | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⚠️ Экстрим |
| **prisma** | Rust | PrismaVeil v5 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | 🔬 Тест |
| **burrow** | Go | VLESS+Reality/Hy2 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ✅ |
| **geph4** | Rust | Multi-level | ⭐⭐⭐⭐ | ⭐⭐⭐ | ✅ |
| **snowflake** | Go | WebRTC | ⭐⭐⭐ | ⭐⭐ | ✅ |
| **yggdrasil** | Go | Mesh IPv6 | ⭐⭐⭐ | ⭐⭐⭐ | ⚠️ |

### Рекомендации по выбору новых инструментов

| Use case | Рекомендация |
|----------|-------------|
| Production VPN | burrow (простой деплой), geph4 (многоуровневый) |
| Экстремальная цензура | nooshdaroo (DNS), fraud-bridge (ICMP) |
| Исследование/эксперимент | prisma (современный стек) |
| Резервный канал | snowflake (WebRTC), yggdrasil (mesh) |
| Не требует домена/сертификата | mieru, fraud-bridge |

---

## 7. Архитектурные паттерны

### 7.1 Мульти-серверная архитектура

```
Пользователь → VLESS Config с массивом серверов:
  [
    {address: "moscow.server.com", port: 443, ...},  — низкий ping для РФ
    {address: "germany.server.com", port: 443, ...},  — доступ к EU контенту
    {address: "nl.server.com", port: 443, ...}        — резерв
  ]

Клиент (Happ/V2RayNG) автоматически переключается между серверами.
```

**Стратегии выбора:**
- `url-test` — автоматический выбор по latency
- `fallback` — основной → резерв при недоступности
- `load-balance` — round-robin / sticky-session

### 7.2 Proxy Chains (Multi-hop)

```
Client → Entry Node (Россия) → Exit Node (NL) → Internet

DPI видит: TLS к российскому серверу (легитимный)
Exit Node видит: трафик от Entry Node (IP скрыт)
```

**Преимущества:** IP пользователя скрыт от exit node, IP exit node скрыт от DPI.
**Недостатки:** Double latency, сложнее настраивать.

### 7.3 Split Tunneling

**По доменам:**
```
*.vk.ru, *.yandex.ru, *.gosuslugi.ru → DIRECT
*.youtube.com, *.google.com, *.github.com → PROXY
```

**По IP (GeoIP):**
```
RU IP → DIRECT
Non-RU IP → PROXY
```

**По приложению:** Только браузер через VPN, остальное — напрямую.

### 7.4 Fallback Mechanisms

```
1. VLESS+Reality (primary) → работает? → OK
2. Hysteria2 (fallback)     → работает? → OK
3. AmneziaWG (fallback)     → работает? → OK
4. DIRECT (last resort)
```

**Автоматическое переключение:**
- Health check каждые 60 секунд
- 3 failures → switch to next protocol
- Recovery check каждые 5 минут

### 7.5 Redundancy Patterns

**Active-Active:** Все серверы работают одновременно, балансировка нагрузки.
**Active-Passive:** Один основной, остальные в горячем резерве.
**Geographic:** Серверы в разных юрисдикциях, DNS-based routing.

---

## 8. Продвинутые техники

### 8.1 Domain Fronting (исторический контекст)

```
SNI: cdn.example.com (видит DPI)
Host: blocked-service.com (внутри TLS)

Статус: ❌ Не работает с 2018-2020 (CDN блокируют)
```

### 8.2 Encrypted Client Hello (ECH)

**RFC 9460** — шифрование SNI в TLS 1.3:

```
ClientHelloOuter:
  SNI: public-site.com (видит DPI — легитимный)
ClientHelloInner (encrypted):
  SNI: actual-service.com (реальный)

Требует: DNS HTTPS RR + ECH config, Chrome 117+, Firefox 118+
```

**Ограничения:** DNS-блокировка предотвращает получение ECH config, не все серверы поддерживают.

### 8.3 Reality Deep Dive

**Принцип:** Использование чужого TLS-сертификата для handshake.

```
1. Клиент → ClientHello с SNI=target-site.com
2. Reality сервер → proxy к target-site.com, получает сертификат
3. Сервер → Certificate(target-site.com) → Клиент
4. DPI видит: легитимное TLS-соединение с target-site.com
5. Если неавторизованный клиент → сервер проксирует на target-site.com (fallback)
```

**Ключевые параметры:**
- `dest` — целевой сайт для маскировки (должен поддерживать TLS 1.3)
- `shortIds` — идентификаторы для авторизованных клиентов
- `privateKey/publicKey` — ключи X25519

**Выбор target-сайта:** Крупные HTTPS-сайты с TLS 1.3: `www.microsoft.com`, `www.apple.com`, `www.amazon.com`, `dl.google.com`.

### 8.4 UDP Tunneling через HTTP

Инкапсуляция UDP-трафика в HTTP/2 или HTTP/3 streams. Используется TrustTunnel (`_udp2` pseudo-host) и некоторыми другими протоколами.

### 8.5 ICMP Tunneling

Инкапсуляция данных в ICMP Echo Request/Reply пакеты. Используется fraud-bridge. DPI обычно не блокирует ICMP (ping), поэтому это работающий метод для экстремальных сценариев.

**Ограничения:** Низкая скорость (~100 KB/s), высокий overhead, может блокироваться rate limiting на ICMP.

---

## 9. Сравнительные таблицы

### 9.1 Основные характеристики всех инструментов

| Инструмент | Язык | Протоколы | Multi-server | Route rules | Сложность |
|------------|------|-----------|-------------|-------------|-----------|
| **Xray-core** | Go | VLESS, VMess, Trojan, SS | ✅ Observatory+Balancer | ✅ Routing rules | ⭐⭐⭐ |
| **Mihomo** | Go | VLESS, VMess, Trojan, Hy2, TUIC | ✅ Proxy Groups | ✅ Rule-based | ⭐⭐⭐ |
| **Sing-box** | Go | VLESS, VMess, Trojan, Hy2, TUIC | ✅ URLTest/Selector | ✅ Rule-based | ⭐⭐⭐ |
| **AmneziaWG** | Go/C | WireGuard+obfs | ❌ | ❌ | ⭐⭐ |
| **TrustTunnel** | Rust | HTTP/2, HTTP/3 | ✅ Multi-addr | ❌ | ⭐⭐⭐ |
| **Leaf** | Rust | VLESS, VMess, Trojan, SS | ✅ MPTP | ✅ Chain/Failover | ⭐⭐⭐⭐ |
| **Cloudflare Warp** | Go/Rust | WireGuard, MASQUE | ✅ (глобальная сеть) | ❌ | ⭐ |
| **burrow** | Go | VLESS+Reality, Hy2, SS2022 | ✅ | ❌ | ⭐ |

### 9.2 Эффективность обхода DPI в России (2026)

| Протокол | Устойчивость | Скорость | Рекомендация |
|----------|-------------|----------|-------------|
| VLESS+Reality | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | **Основной выбор** |
| TrustTunnel | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | Резервный |
| Hysteria2 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | Быстрый резерв |
| AmneziaWG | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | Для простых юзеров |
| Mihomo/Sing-box | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | Клиентский routing |
| Cloudflare Warp | ⭐⭐ | ⭐⭐⭐ | ❌ Не для РФ |
| WireGuard | ⭐ | ⭐⭐⭐⭐⭐ | ❌ Блокируется |
| OpenVPN | ⭐ | ⭐⭐⭐⭐ | ❌ Блокируется |

### 9.3 Иерархия протоколов по устойчивости в России (2026)

```
Tier 1 (Максимальная):  VLESS+Reality, TrustTunnel
Tier 2 (Высокая):      Hysteria2, AmneziaWG
Tier 3 (Средняя):      Trojan+Reality, Shadowsocks+plugin
Tier 4 (Низкая):       WireGuard, OpenVPN — блокируются
```

---

## 10. Мониторинг и детектирование блокировок

### 10.1 Признаки блокировки

| Признак | Описание | Проверка |
|---------|----------|---------|
| Connection timeout | Нет ответа от сервера | `curl -v --connect-timeout 5` |
| TLS handshake failure | Обрыв на handshake | Wireshark capture |
| DNS poisoning | Неверный IP в ответе | `dig @8.8.8.8 domain.com` vs `dig @local domain.com` |
| Slow speeds | Throttling через DPI | Speed test через VPN vs напрямую |
| Protocol-specific | Один протокол не работает | Test с другим протоколом |

### 10.2 Методы детектирования

1. **Connectivity test** — `curl`/`wget` к серверу напрямую
2. **DNS verification** — сравнить DNS-ответы от разных серверов
3. **TLS analysis** — Wireshark capture TLS handshake
4. **Protocol rotation** — переключить протокол и проверить
5. **Multi-path test** — попробовать разные маршруты (VPN, Tor, proxy)

### 10.3 Response strategies

1. **Immediate:** Переключить на резервный протокол/сервер
2. **Short-term:** Обновить конфигурацию (новые порты, transport)
3. **Long-term:** Добавить новые серверы, мигрировать протоколы

---

## 11. Антипаттерны

### 11.1 Распространённые ошибки

| Ошибка | Почему плохо | Правильно |
|--------|-------------|-----------|
| Один протокол | Единственная точка отказа | Минимум 2 протокола (VLESS+Reality + Hysteria2) |
| Один сервер | Нет failover | Минимум 2 сервера в разных локациях |
| WireGuard без обфускации | DPI детектирует | AmneziaWG или VLESS+Reality |
| Hardcoded IP | Быстрая блокировка | Домен + DNS-over-HTTPS |
| Нет мониторинга | Не узнаете о блокировке | Health checks + алерты |
| Самоподписанный сертификат | SNI виден DPI | Reality или Let's Encrypt |
| Статичные порты | Легко заблокировать | Стандартные порты (443, 80) |
| Без fallback | Пользователи без VPN | Автоматическое переключение |

### 11.2 Anti-patterns summary

1. ❌ **"Один сервер хватит"** — нет, нужен failover
2. ❌ **"WireGuard самый быстрый"** — да, но блокируется
3. ❌ **"Сертификат не нужен"** — Reality не требует, но без него SNI виден
4. ❌ **"Стандартный порт безопаснее"** — да, но не единственный фактор
5. ❌ **"Можно обойтись без мониторинга"** — нельзя, блокировки происходят внезапно

---

## 12. Рекомендации для rs8kvn_bot

### 12.1 Текущая архитектура

```
Telegram Bot (Go) → 3x-ui Panel → VLESS+Reality+Vision Server → Клиент (Happ)
```

**Ограничение:** 1 установка 3x-ui = 1 сервер. Нет многосерверности из коробки.

### 12.2 Рекомендуемые улучшения

#### Приоритет 1: Мульти-серверность (критично!)

Подписка должна включать несколько серверов:

```go
type MultiServerManager struct {
    servers []ServerConfig
    current int
    mu      sync.RWMutex
}

type ServerConfig struct {
    ID       string
    Country  string
    Protocol string
    Endpoint string
    Priority int
    Status   string // "active", "down", "maintenance"
}
```

Клиент (Happ/V2RayNG) автоматически переключается. Не нужно менять бота — только генератор подписок.

#### Приоритет 2: Резервный протокол

Добавить Hysteria2 или TrustTunnel как fallback:

```go
type ProtocolManager struct {
    primaryProtocol   string // "vless-reality"
    fallbackProtocol  string // "hysteria2"
    trusttunnelConfig *TrustTunnelConfig
}
```

#### Приоритет 3: AmneziaVPN для обычных пользователей

Для нетехнических пользователей — рекомендовать AmneziaVPN с AmneziaWG протоколом. Простой GUI, работает из коробки.

### 12.3 Чек-лист для внедрения

1. [ ] Генератор многосерверных подписок (1-2 дня)
2. [ ] Fallback протокол Hysteria2/TrustTunnel (2-3 дня)
3. [ ] AmneziaVPN конфигурация для пользователей (1 день)
4. [ ] Health check серверов + алерты (1 день)
5. [ ] Автоматическое переключение при блокировке (2-3 дня)

---

## 13. Ссылки и ресурсы

### 13.1 Официальные ресурсы

| Проект | URL |
|--------|-----|
| Xray-core | https://github.com/XTLS/Xray-core |
| Mihomo | https://github.com/MetaCubeX/ClashMeta |
| Sing-box | https://github.com/SagerNet/sing-box |
| AmneziaVPN | https://github.com/amnezia-vpn/amneziavpn |
| TrustTunnel | https://github.com/AdguardTeam/TrustTunnel |
| Leaf | https://github.com/eycorsican/leaf |
| Hysteria2 | https://github.com/apernet/hysteria |
| Cloudflare Warp | https://developers.cloudflare.com/warp-client/ |

### 13.2 Новые инструменты

| Проект | URL |
|--------|-----|
| mieru | https://github.com/enfein/mieru |
| fraud-bridge | https://github.com/stealth/fraud-bridge |
| nooshdaroo | https://github.com/RostamVPN/nooshdaroo |
| prisma | https://github.com/Yamimega/prisma |
| burrow | https://github.com/FrankFMY/burrow |
| geph4 | https://github.com/geph-official/geph4 |
| snowflake | https://gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/snowflake |
| yggdrasil | https://github.com/yggdrasil-network/yggdrasil-go |

### 13.3 GUI Клиенты

| Клиент | Платформа | URL |
|--------|-----------|-----|
| Happ | Android, iOS | https://happ.online |
| v2rayNG | Android | https://github.com/2dust/v2rayNG |
| NekoBox | Android | https://github.com/MatsuriDayo/NekoBox |
| Shadowrocket | iOS (paid) | App Store |
| Stash | iOS | App Store |

### 13.4 Полезные ресурсы

- **EHU DPI Test:** https://echo.eff.org — тест на блокировки
- **OONI Probe:** https://ooni.org — измерение интернет-цензуры
- **Censorship Wiki:** https://censorship.wiki — методы обхода
- **V2Ray Documentation:** https://www.v2ray.com
- **Xray-core Reality Guide:** https://xtls.github.io

---

> **Источник:** Консолидировано из bypass_methods.md, bypass_software_comparison.md, bypass_new_software_2024.md
> **Дата консолидации:** Апрель 2026
