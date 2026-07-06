# Git Workflow — rs8kvn_bot

## Terminal tool (opencode)
Параметр `cd`: используй `.` (корень активного проекта) или `workdir` в bash.

**Правила:**
- `.` или `workdir` = корень проекта ✅
- Абсолютные пути (`/home/kereal/rs8kvn_bot`) — **НЕ** используй в `cd`, вызывают worktree error. В `workdir` — ок.
- Каждый вызов = новый shell, состояние не сохраняется.
- Для долгих команд указывай `timeout_ms` (например `120000`).
- **НЕ** запускай серверы без `timeout_ms` — зависнут.
- **НЕ** используй shell-подстановки (`$VAR`, `$(...)`).
- **НЕ** делай `cd` внутри команды.

## Стандартный workflow

```bash
# 1. Перед началом
git stash              # если есть локальные изменения
git pull --ff-only     # пульнуть без мерджа
git stash pop          # вернуть локальные изменения

# 2. Создать ветку
git checkout -b <type>/<short-description>

# 3. Коммит
git add -A
git commit -m "<type>: <description>"

# 4. Push
git push -u origin <branch-name>
```

## Ветвление
- **`main`** — продакшн. Только через PR.
- **`dev`** — основная ветка разработки (опционально).
- **Фича-ветки**: `<type>/<description>`, например:
  - `feature/sources-table` (текущая)
  - `fix/trial-referrer-cache-increment`
  - `perf/test-speed-optimization`
  - `docs/fix-readme`
  - `refactor/handler-decomposition`
- **Типы**: `feat`, `fix`, `perf`, `docs`, `refactor`, `test`, `chore`, `audit`.

## Коммит-сообщения
- Формат: `<type>: <description>` (lowercase).
- Conventional Commits.
- Подробные bullet lists через `-` в теле.
- Пример:
  ```
  fix: increment referrer cache on trial bind (was stale up to 1h)
  
  - internal/bot/command.go (handleBindTrial): добавлен
    c.h.IncrementReferralCount(sub.ReferredBy) внутри
    if sub.ReferredBy > 0 после уведомления
  - internal/bot/commands_test.go: TestHandleBindTrial_IncrementsReferrerCacheCount
    (падал до, passing после) + regression guard
  ```

## Pull Requests
- После push GitHub предложит создать PR по ссылке из вывода.
- Влить в `main` (или `dev` → `main`).

## Stash при конфликтах
```bash
git stash
git pull --ff-only
git stash pop    # auto-merges если возможно
```

## Полезные команды
```bash
git status                    # состояние
git diff --stat               # список изменённых файлов
git log --oneline -10         # последние коммиты
git branch -a                 # все ветки
git branch -d <name>          # удалить локальную ветку
git push origin --delete <name>  # удалить удалённую ветку
```

## Remote
- `origin` → `https://github.com/kereal/rs8kvn_bot.git`
- SSH ключ настроен.

## Текущая активная ветка
- `plans_and_pricing` (merge candidate, ready for v2.3.0)

---

**Обновлено:** 2026-06-28
