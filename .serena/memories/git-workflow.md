## Git Workflow

### ОБЯЗАТЕЛЬНО ПРИ СТАРТЕ РАБОТЫ
1. **Активировать проект Serena**: `activate_project("rs8kvn_bot")`
2. **Проверить onboarding**: `check_onboarding_performed()`
3. **Прочитать памяти**: Прочитать git-workflow, project_overview, code_style

### СТРОГО ЗАПРЕЩЕНО
- ❌ **НЕ коммитить напрямую в main**
- ❌ **НЕ делать push в main**

### Правильный Git Workflow

**Ветки:**
- `main` — production ветка, только через merge из dev
- `dev` — development ветка, можно коммитить напрямую (но лучше через feature ветки)
- `feature/*` — feature ветки для новых фич
- `fix/*` — fix ветки для багфиксов

**Workflow:**
1. Создать feature/fix ветку: `git checkout -b fix/description` или `git checkout -b feature/description`
2. Внести изменения
3. Протестировать: `go test ./...`, `go build ./...`
4. Сделать коммит: `git commit -m "fix: description"`
5. Запушить ветку: `git push origin <branch-name>`
6. **Мержить в dev** (можно напрямую или через PR)
7. Для релиза: merge dev → main

**Коммиты напрямую в dev:**
- ✅ Можно делать небольшие изменения напрямую в dev
- ⚠️ Но лучше использовать feature ветки для всех изменений

### Branch Strategy
- **Main branch**: `main` — только через merge из dev, никаких прямых коммитов
- **Dev branch**: `dev` — development ветка, можно коммитить напрямую (но лучше через ветки)
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

### Pre-commit Checklist
- `go build ./...` — компиляция проходит
- `go test ./... -count=1 -timeout 180s` — все тесты проходят
- `golangci-lint run ./...` — нет lint ошибок
- Нет секретов/credentials в коммитах

### Пример правильного workflow
```bash
# 1. Активировать проект (ИИ должен сделать это первым делом)
activate_project("rs8kvn_bot")

# 2. Проверить onboarding
check_onboarding_performed()

# 3. Создать ветку
git checkout -b fix/smoke-test-timeout

# 4. Внести изменения и протестировать
go test ./tests/smoke/...

# 5. Коммит
git add <files>
git commit -m "fix: description"

# 6. Push
git push origin fix/smoke-test-timeout

# 7. Создать PR
gh pr create --title "fix: description" --body "..."

# 8. Ждать review
```