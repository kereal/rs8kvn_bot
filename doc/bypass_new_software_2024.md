# Новые и малоизвестные инструменты обхода блокировок (2024-2025)

## Обзор

Документ содержит анализ новых и малоизвестных инструментов для обхода интернет-цензуры. Все инструменты выбраны по критериям:
- Инновационные подходы к обфускации
- Активная разработка (2024-2025)
- Техническая уникальность
- Потенциал для обхода DPI в России

---

## 1. mieru (Go)

**Репозиторий:** `enfein/mieru`  
**Звёзды:** 1625  
**Язык:** Go  
**Лицензия:** GPL-3.0

### Технические характеристики

**Тип:** SOCKS5 / HTTP / HTTPS прокси

**Уникальные особенности:**
- Не использует TLS протокол - не требует домен или сертификат
- Шифрование XChaCha20-Poly1305 с генерацией ключей на основе username, password и system time
- Random padding и replay attack detection
- Поддержка множества пользователей на одном сервере
- IPv4 и IPv6 поддержка

### Архитектура

```
Client (mieru) ←→ [Encrypted Channel] ←→ Server (mita) ←→ Internet
                        ↓
            No TLS handshake
            No domain required
            No certificate needed
```

### Протокол

**Handshake:**
1. Клиент генерирует ключ из (username, password, timestamp)
2. Шифрует initial packet с XChaCha20-Poly1305
3. Добавляет random padding для маскировки размера
4. Сервер проверяет replay attacks

**Traffic patterns:**
- Случайные размеры пакетов
- Variable timing для избежания fingerprinting
- Нет характерных TLS паттернов

### Преимущества

✅ Не детектируется как TLS traffic  
✅ Не требует домен/сертификат  
✅ Простая настройка  
✅ Multi-user support  
✅ Активная разработка (85+ releases)

### Недостатки

❌ Может детектироваться по статистическим паттернам  
❌ Требует собственного сервера  
❌ Меньше клиентов по сравнению с V2Ray/Xray

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐ (высокая)

**Причины:**
- DPI не распознаёт как VPN/прокси
- Нет характерного TLS fingerprint
- Traffic выглядит как random data

### Интеграция с другим ПО

**Server:** mita (официальный сервер)  
**Third-party servers:**
- mihomo
- sing-box (fork: enfein/mbox)

**Clients:**
- Desktop: Clash Verge Rev, Mihomo Party, NyameBox
- Android: ClashMetaForAndroid, ClashMi, NekoBoxForAndroid
- iOS: ClashMi, Karing

---

## 2. fraud-bridge (C++)

**Репозиторий:** `stealth/fraud-bridge`  
**Звёзды:** 228  
**Язык:** C++  
**Лицензия:** Open source

### Технические характеристики

**Тип:** ICMP / DNS / NTP туннелирование

**Поддерживаемые протоколы:**
- ICMP (IPv4)
- ICMPv6
- DNS (UDP IPv4/IPv6)
- NTP (UDP IPv4/IPv6)

### Архитектура

```
[Inside host] ←ICMP/DNS/NTP→ [Outside server] → Internet
       ↓                              ↓
   TUN interface              Point-to-Point tunnel
       ↓                              ↓
   1.2.3.4 ←──────────────────→ 1.2.3.5
```

### Технические детали

**ICMP туннелирование:**
```
ICMP Echo Request (Type 8):
  ├─ Header: 8 bytes
  ├─ HMAC-MD5: 16 bytes (integrity)
  └─ Payload: encrypted data

Overhead: 24 bytes only
```

**DNS туннелирование:**
```
DNS Query:
  ├─ EDNS0 extension headers
  ├─ TXT records for data
  └─ Up to 1232 bytes per packet

MSS clamping for performance
```

**NTP туннелирование:**
```
NTP packets:
  ├─ UDP-based
  ├─ Works through CGN
  └─ May require smaller MSS (100 bytes) for some ISPs
```

### Уникальные возможности

- **MSS clamping:** Автоматическая настройка для избежания фрагментации
- **HMAC-MD5 integrity:** Защита от injected packets
- **Roaming support:** SSH sessions выживают при смене IP (WiFi ↔ 4G)
- **Chroot support:** Security hardening

### Преимущества

✅ Работает когда TCP/UDP заблокированы  
✅ Выглядит как легитимный ping/DNS traffic  
✅ Поддержка IPv4 и IPv6  
✅ Работает за NAT  
✅ Roaming/mobility support

### Недостатки

❌ Низкая производительность (ограничен MTU)  
❌ High latency  
❌ Некоторые провайдеры блокируют/троттлят ICMP  
❌ Требует root привилегий

### Эффективность в России

**Оценка:** ⭐⭐⭐ (средняя-высокая)

**Рекомендации:**
- Использовать когда другие методы заблокированы
- ICMP: приоритетный выбор
- DNS: для fallback когда ICMP заблокирован
- NTP: для специфических сетей

### Use cases

- Экстремальные сценарии блокировок
- Сети с блокировкой TCP/UDP
- Когда DPI блокирует все VPN протоколы
- Для SSH roaming (смена сети без разрыва сессии)

---

## 3. nooshdaroo (Rust)

**Репозиторий:** `RostamVPN/nooshdaroo`  
**Звёзды:** 18  
**Язык:** Rust  
**Лицензия:** MIT  
**Создан:** Март 2026 (очень новый!)

### Технические характеристики

**Тип:** DNS-туннелированный SOCKS5 прокси

**Размер бинарника:** 982KB  
**Зависимости:** Нет внешних зависимостей при runtime

### Протокольный стек

```
Application (browser, curl)
    ↓ SOCKS5
nooshdaroo client
    ↓ smux v2 (stream multiplexing)
    ↓ Noise_NK (authenticated encryption)
    ↓ KCP (reliable transport)
    ↓ DNS queries (base32 in QNAME, TXT RDATA)
Recursive DNS resolver (8.8.8.8)
    ↓
nooshdaroo server (authoritative for tunnel domain)
    ↓ SOCKS5
Internet
```

### Криптография

**Noise_NK Protocol:**
- `Noise_NK_25519_ChaChaPoly_BLAKE2s`
- X25519 для key exchange
- ChaCha20-Poly1305 для шифрования
- BLAKE2s для аутентификации

**Key management:**
```
Server generates keypair:
  privkey: 32 bytes hex
  pubkey:  32 bytes hex (shared with client)

Client authenticates server via pre-shared pubkey
```

### DNS Fingerprinting (Chrome mimicry)

**Chrome DNS behaviour emulation:**
- AD=1 flag (Authenticated Data)
- EDNS0 UDP size: 1452 (Chrome default, не 4096)
- A+AAAA+HTTPS query pairs
- Burst timing: 5-15 queries при page load, затем silence

**Cover traffic:**
```
Real tunnel queries + Chrome DNS cover traffic
    ↓
DPI видит нормальный DNS resolution паттерн
```

### DNS Flux

**Multi-domain support:**
```
Configured domains:
  ├─ t.example.com
  ├─ t.backup.example.com
  └─ t.another.example.com

Time-based selection (6-hour periods):
  Период 1: domains[0:2]
  Период 2: domains[2:4]
  ...
```

### OTA Config Updates

**Dynamic configuration:**
- Config шифруется ChaCha20-Poly1305
- Chunked в DNS TXT records
- Обновление без нового binary release

### Преимущества

✅ Extremely stealthy (DNS looks legitimate)  
✅ Small binary (982KB)  
✅ No external dependencies  
✅ Chrome DNS fingerprinting  
✅ Forward secrecy (Noise protocol)  
✅ Wire-compatible с dnstt (Go)

### Недостатки

❌ Low throughput (DNS overhead)  
❌ Higher latency  
❌ Requires DNS infrastructure  
❌ Very new project (limited testing)  
❌ Few users/production deployments

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐ (высокая)

**Рекомендации:**
- Для экстремальных сценариев
- Когда все TCP/UDP заблокированы
- Сценарии с глубокой инспекцией TLS

### Настройка DNS

**Required DNS setup:**
```
t.example.com.  NS  ns.example.com.
ns.example.com. A   <your-server-ip>
```

**Server config:**
```json
{
  "server_id": "my-server",
  "listen": "0.0.0.0:53",
  "privkey": "<hex>",
  "upstream": "socks5",
  "domains": ["t.example.com"]
}
```

---

## 4. prisma (Rust)

**Репозиторий:** `Yamimega/prisma`  
**Звёзды:** 0  
**Язык:** Rust  
**Лицензия:** GPL-3.0  
**Создан:** Март 2026 (очень новый!)

### Технические характеристики

**Тип:** Encrypted proxy infrastructure suite

**Уникальная технология:** PrismaVeil v5 wire protocol

### PrismaVeil v5 Protocol

**Ключевые особенности:**
- 1-RTT handshake
- 0-RTT resumption
- X25519 + BLAKE3 + ChaCha20/AES-256-GCM
- Header-authenticated encryption (AAD)
- Connection migration
- Enhanced KDF

### Транспорты (8 вариантов)

1. **QUIC v2** - UDP-based, HTTP/3
2. **PrismaTLS** - Active probing resistance
3. **WebSocket** - CDN-compatible
4. **gRPC** - HTTP/2 based
5. **XHTTP** - Extended HTTP
6. **XPorta** - Custom protocol
7. **SSH** - SSH transport
8. **WireGuard** - Native WG support

### PrismaTLS (Reality alternative)

**Механизм:**
- Browser fingerprint mimicry
- Dynamic mask server pool
- Active probing resistance
- Без необходимости Reality certificate

**Как работает:**
```
Client → PrismaTLS → [Mimic browser fingerprint]
                    → [Dynamic server selection]
                    → [Active probe resistance]
                    → Server
```

### Traffic Shaping

**Anti-fingerprinting techniques:**
- Bucket padding
- Timing jitter
- Chaff injection
- Frame coalescing

**Цель:** Defeat encapsulated TLS fingerprinting

### Дополнительные возможности

**TUN mode:**
- System-wide proxy
- Windows / Linux / macOS

**GeoIP routing:**
- v2fly geoip.dat
- Client-side и server-side routing
- Country-level smart routing

**Port forwarding:**
- frp-style reverse proxy
- Over encrypted tunnels

**Web console:**
- Next.js + shadcn/ui
- Real-time monitoring

**Native GUI clients:**
- Windows (Win32/GDI)
- Android (Jetpack Compose)
- iOS (SwiftUI)
- macOS (menu bar)

### Преимущества

✅ 8 transport options  
✅ Modern cryptography  
✅ Native GUI clients  
✅ Web console  
✅ Traffic shaping  
✅ PrismaTLS (Reality alternative)  
✅ Cross-platform TUN support

### Недостатки

❌ Very new (0 stars, created March 2026)  
❌ Limited community  
❌ Unknown production readiness  
❌ Documentation in development

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐ (потенциально высокая)

**Рекомендации:**
- Monitor development
- Test in staging environment
- Consider for new deployments
- PrismaTLS as Reality alternative

### Установка

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Yamimega/prisma/master/scripts/install.sh | bash -s -- --setup

# Windows (PowerShell)
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Yamimega/prisma/master/scripts/install.ps1))) -Setup
```

---

## 5. burrow (Go)

**Репозиторий:** `FrankFMY/burrow`  
**Звёзды:** 2  
**Язык:** Go  
**Лицензия:** Apache-2.0  
**Создан:** Март 2026 (новый!)

### Технические характеристики

**Тип:** Self-hosted VPN для censorship circumvention

**Ключевой принцип:** Deploy in one command, connect in one click

### Поддерживаемые протоколы

| Протокол | Порт | Описание |
|----------|------|----------|
| VLESS+Reality | 443/TCP | Primary, маскировка под HTTPS |
| VLESS+WebSocket (CDN) | 8080/TCP | Cloudflare-fronted |
| Hysteria2 | 8443/UDP | QUIC-based |
| Shadowsocks 2022 | 8388/TCP | Modern encryption |
| WireGuard | 51820/UDP | Standard VPN (disabled by default) |

### Архитектура

```
Server (VPS)
├─ Landing Page
├─ Admin Dashboard
├─ Management API
├─ Transport Engine
│   ├─ VLESS+Reality (443)
│   ├─ VLESS+WS/CDN (8080)
│   └─ Hysteria2 (8443)
└─ SQLite DB

Client (Desktop)
├─ Desktop Client (Tauri 2)
│   ├─ Onboarding wizard
│   ├─ VPN mode (TUN)
│   ├─ Proxy mode
│   └─ Kill switch
├─ Tunnel Engine (sing-box)
└─ Client Daemon (HTTP API :9090)
```

### Уникальные возможности

**Invite-only access:**
- HMAC-signed invite links
- Мгновенный revoke доступа
- Нет необходимости в credential management

**Connection fallback:**
```
Client attempts:
  1. Direct connection (VLESS+Reality)
  2. If blocked → CDN WebSocket fallback
  3. If blocked → Hysteria2
  4. If blocked → Shadowsocks 2022
```

**Kill switch:**
- Блокирует весь интернет при падении VPN
- Prevents unprotected browsing
- Platform-native implementation

**Auto-reconnect:**
- Detects dead tunnel
- Exponential backoff (up to 10 attempts)
- Cancelable by user

### Desktop Client Features

- One-click connect
- Live speed stats
- Server ping (latency measurement)
- Server switching without disconnect
- Desktop notifications
- System tray integration
- Auto-connect on launch
- Auto-update
- Single instance
- Deep links (`burrow://connect/...`)
- Localization (EN, RU, ZH)
- Split tunneling

### Server Features

- One-command deploy (Docker)
- Admin dashboard
- Landing page
- Per-user bandwidth limits
- Health metrics
- Security hardened
- DNS leak prevention
- CI/CD pipeline

### API Endpoints

**Server API:**
```
GET  /health                    Liveness check
POST /api/auth/login            Admin login → JWT
POST /api/connect               Client config (token auth)
GET  /api/clients               List all clients
DELETE /api/clients/:id         Revoke client
GET  /api/invites               List invites
POST /api/invites               Create invite
GET  /api/stats                 Server statistics
POST /api/rotate-keys           Rotate Reality keys
```

**Client Daemon API:**
```
GET  /api/status                Connection status
POST /api/connect               Start VPN tunnel
POST /api/disconnect            Stop VPN tunnel
GET  /api/servers               List servers
POST /api/servers               Add server from invite
GET  /api/servers/:name/ping   Measure latency
```

### Преимущества

✅ Excellent UX (one-click deploy/connect)  
✅ Multi-protocol with auto-fallback  
✅ Modern desktop client (Tauri 2)  
✅ Kill switch  
✅ Split tunneling  
✅ Auto-update  
✅ Well-documented  
✅ Security hardened

### Недостатки

❌ Very new (2 stars, March 2026)  
❌ Limited community  
❌ Requires own server  
❌ Mobile clients not ready (scaffold only)

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐⭐ (очень высокая)

**Причины:**
- VLESS+Reality как primary protocol
- CDN fallback для IP blocking
- Multi-protocol support
- Professional implementation

### Рекомендуемое использование

**Для кого:**
- Пользователи, которым нужна простота
- Команды, которым нужен private VPN
- Проекты, требующие self-hosted решение
- Technical users, wanting control

**Deployment:**
```bash
git clone https://github.com/FrankFMY/burrow.git
cd burrow
docker compose up -d
```

---

## 6. geph4-client (Rust)

**Репозиторий:** `geph-official/geph4-client`  
**Звёзды:** 3027  
**Язык:** Rust  
**Лицензия:** Open source

### Технические характеристики

**Тип:** Modular Internet censorship circumvention system

**Название:** Geph (迷霧通)

### Уникальный подход

**Модульная архитектура:**
- Независимые модули для разных функций
- Plug-and-play components
- Легко добавлять новые протоколы

**Названия протоколов:**
- Использует собственные названия
- Не раскрывает детали реализации публично
- Proprietary-ish approach

### Особенности

- Designed specifically for national filtering
- Active development since 2020
- Large user base in censored regions
- Multiple client platforms

### Преимущества

✅ Proven track record (since 2020)  
✅ Large user base  
✅ Modular design  
✅ Multiple platforms  
✅ Active development

### Недостатки

❌ Less transparent than other solutions  
❌ Custom protocols (harder to audit)  
❌ Limited technical documentation publicly  
❌ May require proprietary components

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐ (высокая)

**Рекомендации:**
- Хорошо известен в сообществе
- Проверенное решение
- Рекомендуется для пользователей с высоким risk profile

---

## 7. snowflake (Go)

**Репозиторий:** `keroserene/snowflake`  
**Звёзды:** 291  
**Язык:** Go  
**Лицензия:** Open source (Tor Project)

### Технические характеристики

**Тип:** WebRTC Pluggable Transport для Tor

**Разработчик:** Tor Project

### Как работает

```
Tor Client (with snowflake)
    ↓ WebRTC connection
Snowflake proxy (browser volunteer)
    ↓
Tor network
    ↓
Exit node
    ↓
Internet
```

**WebRTC advantages:**
- P2P connections
- NAT traversal (STUN/TURN)
- Looks like video call traffic
- Hard to block without blocking all WebRTC

### Компоненты

1. **Client:** Tor client с snowflake support
2. **Proxy:** Browser extension / web page volunteer
3. **Broker:** Matches clients with proxies
4. **STUN servers:** NAT traversal

### Волонтёрская сеть

**Как это работает:**
1. Волонтёры открывают https://snowflake.torproject.org/
2. Браузер становится snowflake proxy
3. Клиенты Tor используют эти proxy для подключения
4. DPI видит WebRTC traffic, не Tor

### Преимущества

✅ Part of Tor ecosystem  
✅ Distributed proxy network (no single point of failure)  
✅ WebRTC traffic looks legitimate  
✅ Easy to volunteer (just open webpage)  
✅ Works in highly censored networks

### Недостатки

❌ Only for Tor (not general VPN)  
❌ Dependent on volunteers  
❌ Variable performance  
❌ Can be slow  
❌ Requires Tor client

### Эффективность в России

**Оценка:** ⭐⭐⭐⭐ (высокая для Tor access)

**Рекомендации:**
- Для доступа к Tor в цензурированных сетях
- Использовать вместе с другими мостами
- Для анонимности, не просто обхода блокировок

---

## 8. yggdrasil-go (Go)

**Репозиторий:** `yggdrasil-network/yggdrasil-go`  
**Звёзды:** 4882  
**Язык:** Go  
**Лицензия:** LGPL-3.0

### Технические характеристики

**Тип:** Encrypted IPv6 overlay network

**Подход:** Mesh networking с end-to-end шифрованием

### Архитектура

```
Node A (Yggdrasil)
    ↓ Encrypted tunnel
Node B (Yggdrasil)
    ↓ Encrypted tunnel
Node C (Yggdrasil)
    ↓
Yggdrasil Network (IPv6 overlay)
    ↓
Internet services on Yggdrasil
```

### Ключевые особенности

**Mesh routing:**
- Decentralised routing algorithm
- Spanning tree protocol variant
- Self-healing network
- No central servers

**IPv6 overlay:**
- Each node gets IPv6 address
- End-to-end connectivity
- Can run services on Yggdrasil
- Transparent to applications

**Encryption:**
- End-to-end encryption
- Curve25519 key exchange
- ChaCha20-Poly1305 encryption
- Forward secrecy

### Use cases

- Decentralised services
- Mesh networking
- Access to Yggdrasil-only services
- Bypass censorship via overlay network
- IoT networks
- Community networks

### Преимущества

✅ Fully decentralised  
✅ End-to-end encryption  
✅ Self-healing mesh  
✅ Works through NAT  
✅ IPv6 overlay (transparent to apps)  
✅ Large community (4882 stars)  
✅ Active development

### Недостатки

❌ Not designed specifically for censorship circumvention  
❌ Requires other peers to be useful  
❌ Performance depends on network topology  
❌ Limited services on Yggdrasil network  
❌ Not a general-purpose VPN

### Эффективность в России

**Оценка:** ⭐⭐⭐ (средняя)

**Рекомендации:**
- Для доступа к Yggdrasil-only сервисам
- Для mesh networking проектов
- Не как primary censorship circumvention tool
- Комбинировать с другими решениями

---

## Сравнительная таблица

| Инструмент | Язык | Тип | Звёзды | Новизна | Скрытность | Производительность |
|-----------|------|-----|--------|---------|------------|-------------------|
| mieru | Go | SOCKS5/HTTP proxy | 1625 | 2021+ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| fraud-bridge | C++ | ICMP/DNS/NTP tunnel | 228 | 2013+ | ⭐⭐⭐⭐⭐ | ⭐⭐ |
| nooshdaroo | Rust | DNS-tunneled SOCKS5 | 18 | 2026 | ⭐⭐⭐⭐⭐ | ⭐⭐ |
| prisma | Rust | Proxy infrastructure | 0 | 2026 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| burrow | Go | Self-hosted VPN | 2 | 2026 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| geph4-client | Rust | Circumvention system | 3027 | 2020+ | ⭐⭐⭐⭐ | ⭐⭐⭐ |
| snowflake | Go | Tor pluggable transport | 291 | 2015+ | ⭐⭐⭐⭐ | ⭐⭐ |
| yggdrasil-go | Go | IPv6 overlay network | 4882 | 2017+ | ⭐⭐⭐ | ⭐⭐⭐ |

---

## Рекомендации по выбору

### По use case

| Сценарий | Рекомендуемый инструмент | Причина |
|----------|------------------------|---------|
| **Обычный пользователь** | burrow | Простота, один клик |
| **Продвинутый пользователь** | mieru или prisma | Баланс скрытности и скорости |
| **Экстремальные блокировки** | nooshdaroo или fraud-bridge | DNS/ICMP туннелирование |
| **Tor доступ** | snowflake | WebRTC masking |
| **Mesh networking** | yggdrasil-go | Децентрализованная сеть |
| **Проверенное решение** | geph4-client | Большой user base |

### По threat model

| Threat Level | Рекомендация |
|-------------|-------------|
| **Низкий** | mieru, prisma |
| **Средний** | burrow, geph4-client |
| **Высокий** | nooshdaroo, fraud-bridge |
| **Критический** | fraud-bridge (ICMP) + nooshdaroo (DNS) multi-layer |

### По региону

| Регион | Рекомендация | Обоснование |
|--------|-------------|-------------|
| **Россия** | burrow, mieru, prisma | VLESS+Reality работает, альтернативы готовы |
| **Китай** | geph4-client, nooshdaroo | Проверенные решения, DNS туннелирование |
| **Иран** | nooshdaroo, fraud-bridge | DNS/ICMP работают в большинстве сетей |
| **Турция** | mieru, prisma | Меньше DPI, достаточно стандартных методов |

---

## Технические рекомендации

### Для production использования

**Primary stack:**
```
burrow (VLESS+Reality)
    ↓ fallback
mieru (non-TLS encryption)
    ↓ fallback
nooshdaroo (DNS tunneling)
```

**Причины:**
- Multi-layer resilience
- Different traffic patterns
- Automatic fallback

### Для разработки

**Интересные проекты для contribution:**
1. **prisma** - новый, активная разработка, Rust
2. **nooshdaroo** - новый, Rust, DNS tunneling
3. **burrow** - новый, Go, excellent UX
4. **mieru** - зрелый, Go, много пользователей

### Для исследования

**Areas to explore:**
- Traffic shaping techniques (prisma)
- DNS fingerprinting evasion (nooshdaroo)
- Multi-protocol fallback (burrow)
- Non-TLS encryption (mieru)

---

## Заключение

### Самые перспективные проекты (2024-2025)

1. **burrow** - лучший UX, multi-protocol, production-ready
2. **prisma** - инновационный протокол, Rust, активная разработка
3. **nooshdaroo** - уникальный DNS tunneling, Rust, крайне скрытный

### Тренды

1. **Rust adoption** - новые проекты предпочитают Rust (nooshdaroo, prisma)
2. **Multi-protocol support** - один инструмент, несколько протоколов (burrow, prisma)
3. **UX focus** - упрощение для end users (burrow: one-command deploy)
4. **Non-TLS approaches** - mieru, nooshdaroo не используют TLS
5. **DNS tunneling revival** - nooshdaroo показывает новый подход

### Для rs8kvn_bot проекта

**Рекомендуемые добавления:**

1. **mieru** - как альтернатива VLESS+Reality
   - Не требует сертификатов
   - Простая интеграция
   - Активная разработка

2. **burrow** - для users, которым нужна простота
   - One-click setup
   - Invite system
   - Multi-protocol

3. **nooshdaroo** - для экстремальных случаев
   - DNS tunneling
   - Chrome fingerprinting
   - Fallback option

---

*Документ подготовлен для опытных сетевых инженеров и системных программистов. Актуальность: март 2024-2025.*