# Git Workflow & Команды

## ОБЯЗАТЕЛЬНО ПРИ СТАРТЕ РАБОТЫ
1. **Активировать проект Serena**: `activate_project("rs8kvn_bot")`
2. **Проверить onboarding**: `check_onboarding_performed()`
3. **Прочитать памяти**: Прочитать git-workflow, project_overview, code_style

---

## Terminal Tool Usage

### Важно: Формат пути к директории

При использовании `terminal` tool параметр `cd` должен использовать **basename** корневой директории проекта, а НЕ абсолютный путь.

#### Правильный формат:
```
cd: "rs8kvn_bot"  # ✅ ПРАВИЛЬНО — use basename
```

#### Неправильный формат:
```
cd: "/home/kereal/rs8kvn_bot"  # ❌ НЕПРАВИЛЬНО — абсолютный путь вызывает "not in any of the project's worktrees" error
cd: "."  # ❌ НЕПРАВИЛЬНО — ambiguous в multi-root workspaces
```

### Информация о проекте
- Имя проекта: `rs8kvn_bot`
- Абсолютный путь: `/home/kereal/rs8kvn_bot`
- **Terminal cd параметр**: `rs8kvn_bot` (basename корневой директории)

### Доступные CLI инструменты
- `git` — version control
- `gh` — GitHub CLI (version 2.46.0)

---

## Команды разработки

### Go команды
```bash
# Запуск бота
go run ./cmd/bot

# Сборка бота
go build -o rs8kvn_bot ./cmd/bot

# Запуск тестов
go test -v ./...

# Запуск тестов с покрытием
go test -v ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Запуск линтеров
golangci-lint run
gosec ./...

# Форматирование кода
go fmt ./...
go mod tidy
```

### Docker
```bash
# Сборка образа
docker build -t rs8kvn_bot .

# Запуск через docker-compose
docker-compose up -d
```

### База данных
```bash
# База данных — SQLite, расположена в data/tgvpn.db
# Бэкапы — в директории data/
```

---

## Git Workflow

### СТРОГО ЗАПРЕЩЕНО
- ❌ **НЕ коммитить напрямую в main**
- ❌ **НЕ делать push в main**
- ❌ **НЕ коммитить напрямую в dev** — ВСЕГДА через feature/fix ветку + PR

### Branch Strategy
- **Main branch**: `main` — только через merge из dev, никаких прямых коммитов
- **Dev branch**: `dev` — интеграционная ветка, только через merge из PR
- **Docs branches**: `docs/<description>` — для изменений документации
- **Feature branches**: `feature/<description>` — для новых фич
- **Fix branches**: `fix/<description>` — для багфиксов
- **Remote**: `https://github.com/kereal/rs8kvn_bot.git`

### Commit Conventions (Conventional Commits)
- `feat:` — новая функциональность
- `fix:` — исправление багов
- `refactor:` — рефакторинг без изменения поведения
- `test:` — добавление/изменение тестов
- `docs:` — изменения документации
- `chore:` — рутинные задачи (зависимости, конфиги)
- `style:` — форматирование, без изменения логики
- `perf:` — улучшение производительности
- `ci:` — изменения CI/CD
- `build:` — изменения сборки

**Breaking change:** добавить `!` после типа или `BREAKING CHANGE:` в footer

### Branch Naming
```
feature/TICKET-123-description
fix/TICKET-456-bug-name
release/1.2.0
hotfix/1.2.1-security-patch
```

### Правильный Workflow (ОБЯЗАТЕЛЬНО)
1. Создать ветку: `git checkout -b docs/description` / `fix/description` / `feature/description`
2. Внести изменения
3. Протестировать (см. Pre-commit Checklist)
4. Сделать коммит: `git commit -m "type: description"`
5. Запушить ветку: `git push origin <branch-name>`
6. Создать PR: `gh pr create --base dev --title "type: description" --body "..."`
7. Ждать review → только после одобрения мержить
8. Для релиза: merge dev → main

### Никаких прямых коммитов в dev
- ❌ Даже «небольшие» изменения — через ветку + PR
- ❌ Даже исправление опечатки — через ветку + PR
- ✅ Это обеспечивает audit trail и возможность rollback

### Common Git Commands
```bash
git status
git add .
git commit -m "message"
git push

# Создать ветку
git checkout -b feature/TICKET-123-description

# GitHub CLI (gh)
gh pr create --title "Title" --body "Description"
gh pr list
gh pr merge <number>
gh release create v1.0.0
```

---

## Pre-commit Checklist
- `go build ./...` — компиляция проходит
- `go test ./... -count=1 -timeout 180s` — все тесты проходят
- `golangci-lint run ./...` — нет lint ошибок
- Нет секретов/credentials в коммитах

---

## Пример правильного workflow (ОБЯЗАТЕЛЬНО)
```bash
# 1. Активировать проект
activate_project("rs8kvn_bot")

# 2. Проверить onboarding
check_onboarding_performed()

# 3. Создать ветку (ВСЕГДА, без исключений!)
git checkout -b docs/consolidate-documentation

# 4. Внести изменения и протестировать
go test ./...

# 5. Коммит
git add <files>
git commit -m "docs: description"

# 6. Push
git push origin docs/consolidate-documentation

# 7. Создать PR в dev (ВСЕГДА!)
gh pr create --base dev --title "docs: description" --body "..."

# 8. НЕ МЕРЖИТЬ без review!
```
