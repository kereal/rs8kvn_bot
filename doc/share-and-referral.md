# VPN Trial + Referral Flow — Sequence Diagram

```mermaid
sequenceDiagram

actor Referrer as Referrer (Telegram User)
actor User as New User
participant Bot as Telegram Bot
participant Web as vpn.site
participant DB as Database
participant Panel as 3x-ui Panel

%% =========================
%% INVITE CREATION
%% =========================

Referrer ->> Bot: Request invite link
Bot ->> DB: SELECT * FROM invites WHERE referrer_tg_id = X

alt Invite exists
    Bot -->> Referrer: Show existing https://vpn.site/i/{8-char code}
else No invite
    Bot ->> DB: INSERT invite(code, referrer_tg_id)
    Bot -->> Referrer: Show https://vpn.site/i/{8-char code}
end

%% =========================
%% TRIAL CREATION
%% =========================

User ->> Web: Open /i/{code}

Web ->> DB: SELECT * FROM invites WHERE code={code}

alt Invite not found
    Web -->> User: Error page (404)
else Invite OK

    %% Check for existing trial via cookie (prevents duplication)
    Web ->> Web: Check cookie rs8kvn_trial_{code}
    
    alt Cookie exists (same browser, < 3 hours)
        Web -->> User: Show existing trial page (no new creation)
    else No cookie (new user or expired)
    
        Web ->> DB: COUNT trial_requests WHERE ip=? AND created_at > now-1h

        alt Too many requests (>= TrialRateLimit)
            Web -->> User: Rate limit page (429)
        else Allowed

            Web ->> DB: INSERT trial_requests(ip)

            Web ->> Panel: Create trial client (trafficBytes, expiryTime)
            Panel -->> Web: client created

            Web ->> DB: INSERT subscriptions(subscription_id, invite_code, is_trial=true, telegram_id=0)

            %% Set cookie to prevent duplication on refresh
            Web ->> User: Set cookie rs8kvn_trial_{code}={subID} (3 hours)
            Web -->> User: Show page with Happ link + Telegram activate button
        end
    end
end

%% =========================
%% BIND FLOW
%% =========================

User ->> Bot: /start trial_{subID}

Bot ->> DB: BindTrialSubscription(subID, tgID, username)
Note over Bot,DB: SELECT WHERE subscription_id=? AND is_trial=true AND telegram_id=0
Note over Bot,DB: If found: UPDATE telegram_id=tgID, is_trial=false, referred_by=referrer
Note over Bot,DB: Race protection: once bound, telegram_id != 0, subsequent binds fail

alt Not found or already activated
    Bot -->> User: Error (subscription not found or already activated)
else Bind success

    Bot ->> Panel: UpdateClient (trafficLimitGB, username, comment with referrer)

    Bot -->> User: VPN activated (full traffic limit)

    Bot ->> Bot: Notify admin about new referral activation
end

%% =========================
%% HOURLY CLEANUP
%% =========================

loop Every 1 hour

    DB ->> DB: SELECT trials WHERE is_trial=true AND telegram_id=0 AND created_at < now-TrialDurationHours

    loop For each expired trial
        Bot ->> Panel: DeleteClient(clientID)
    end

    DB ->> DB: DELETE expired trial subscriptions

    DB ->> DB: DELETE old trial_requests (same cutoff)

end
```

# VPN Referral System — ER Diagram

```mermaid
erDiagram

INVITES {
    varchar code PK "8-char alphanumeric"
    bigint referrer_tg_id "INDEX, NOT NULL"
    timestamp created_at
}

SUBSCRIPTIONS {
    uint id PK
    varchar subscription_id "INDEX"
    varchar client_id
    varchar invite_code FK "INDEX, 16 chars max"
    boolean is_trial "INDEX, default false"
    bigint telegram_id "INDEX, 0 = unbound trial"
    bigint referred_by "INDEX"
    int inbound_id
    bigint traffic_limit "default 100GB"
    timestamp expiry_time
    varchar status "default 'active'"
    varchar subscription_url
    timestamp created_at
    timestamp updated_at
    timestamp deleted_at "soft delete"
}

TRIAL_REQUESTS {
    uint id PK
    varchar ip "INDEX, 45 chars max"
    timestamp created_at
}

INVITES ||--o{ SUBSCRIPTIONS : "invite_code"
```

## Связи

* Один invite может создать много trial подписок
* SUBSCRIPTIONS.invite_code → INVITES.code
* SUBSCRIPTIONS.referred_by — это referrer_tg_id пригласившего (копируется из INVITES)
* TRIAL_REQUESTS не связан FK — это rate limit лог
* Неактивированные trial подписки имеют telegram_id = 0 (не NULL — SQLite имеет особенности с NULL)

---

# Subscription State Diagram

```mermaid
stateDiagram-v2

[*] --> TrialCreated

TrialCreated --> Activated : /start trial_{subID} bind success
TrialCreated --> Deleted : cleanup (every 1h, after TrialDurationHours)

Activated --> ActiveUsage : user uses VPN
ActiveUsage --> Expired : subscription expired

Expired --> Renewed : user renews
Renewed --> ActiveUsage

Deleted --> [*]
Expired --> [*]
```

## Описание состояний

### TrialCreated

* subscription создан через web по invite ссылке
* is_trial = true
* telegram_id = 0 (не NULL)
* Трафик: минимум 1 GB, срок: TrialDurationHours (по умолчанию 3ч)
* В xui создаётся клиент с email `trial_{subID}`

### Activated

* bind выполнен через `/start trial_{subID}`
* telegram_id установлен на ID пользователя
* is_trial = false
* referred_by записан (referrer_tg_id из invites)
* В xui: лимит увеличен до TrafficLimitGB (по умолчанию 100GB), username обновлён

### ActiveUsage

* пользователь активно пользуется VPN

### Expired

* срок подписки закончился

### Renewed

* пользователь продлил подписку

### Deleted

* trial не был активирован и удалён hourly cleanup
* Удаляется и из БД, и из xui панели

---

# Главный принцип

Trial → либо Activated → либо Deleted

Нет других веток.

Это делает систему:

* простой
* предсказуемой
* легко масштабируемой

---

# Конфигурация

| Переменная | По умолчанию | Описание |
|-----------|-------------|----------|
| `SITE_URL` | `https://vpn.site` | Базовый URL для ссылок |
| `TRIAL_DURATION_HOURS` | `3` | Время жизни неактивированного trial (1-168) |
| `TRIAL_RATE_LIMIT` | `3` | Макс. trial с одного IP в час (1-100) |
| `TRAFFIC_LIMIT_GB` | `100` | Лимит трафика после активации |
| `XUI_HOST` | — | URL 3x-ui панели |
| `XUI_USERNAME` | — | Логин панели |
| `XUI_PASSWORD` | — | Пароль панели |

---

## Критичные нюансы

### Trial Duplication Prevention

**Проблема:** Пользователь обновляет страницу → создаётся новая trial подписка.

**Решение:** HttpOnly cookie `rs8kvn_trial_{invite_code}` на 3 часа.

**Поведение:**
- ✅ Refresh страницы = та же подписка (кука найдена)
- ✅ Разные пользователи = разные подписки (разные браузеры/куки)
- ✅ Одно устройство = максимум 1 trial за 3 часа
- ✅ После активации (telegram_id != 0) кука не мешает

**Реализация:**
```go
// internal/web/web.go
cookie, err := r.Cookie("rs8kvn_trial_" + code)
if err == nil {
    // Кука есть — найти существующий trial
    existingSub, _ := s.db.GetTrialSubscriptionBySubID(ctx, cookie.Value)
    if existingSub != nil && !existingSub.IsActivated() {
        // Показать существующий, не создавать новый
        return s.renderTrialPage(existingSub, ...)
    }
}

// Создать новый trial и установить куку
http.SetCookie(w, &http.Cookie{
    Name:     "rs8kvn_trial_" + code,
    Value:    subID,
    Path:     "/i/" + code,
    Expires:  time.Now().Add(3 * time.Hour),
    HttpOnly: true,
    Secure:   true,
    SameSite: http.SameSiteStrictMode,
})
```
