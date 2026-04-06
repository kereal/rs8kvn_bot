## Git Workflow

### ОБЯЗАТЕЛЬНО ПРИ СТАРТЕ РАБОТЫ
1. **Активировать проект Serena**: `activate_project` с именем проекта
2. **Проверить onboarding**: `check_onboarding_performed`
3. **Прочитать памяти**: Прочитать git-workflow, project_overview, code_style

### СТРОГО ЗАПРЕЩЕНО
- ❌ **НЕ коммитить напрямую в main**
- ❌ **НЕ делать push в main**
- ❌ **НЕ делать auto-merge без review**

### Правильный Git Workflow
1. Создать feature/fix ветку от main: `git checkout -b fix/description` или `git checkout -b feature/description`
2. Внести изменения
3. Протестировать: `go test ./...`, `go build ./...`
4. Сделать коммит: `git commit -m "fix: description"`
5. Запушить ветку: `git push origin <branch-name>`
6. **Создать Pull Request** через gh CLI или веб-интерфейс
7. Ждать review и одобрения
8. Только после одобрения мержить PR

### Branch Strategy
- **Main branch**: `main` — только через PR, никаких прямых коммитов
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