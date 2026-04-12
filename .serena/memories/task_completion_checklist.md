# Task Completion Checklist

## ⚠️ ПРИ СТАРТЕ РАБОТЫ (ОБЯЗАТЕЛЬНО)
1. Активировать проект Serena: `activate_project("rs8kvn_bot")`
2. Проверить onboarding: `check_onboarding_performed()`
3. Прочитать памяти: `git-workflow`, `project_overview`, `code_style`

---

When a task is completed, follow these steps:

1. **Run Tests**
   ```bash
   go test -v ./...
   ```

2. **Check Test Coverage**
   ```bash
   go test -v ./... -coverprofile=coverage.out
   go tool cover -func=coverage.out
   ```

3. **Run Linters**
   ```bash
   golangci-lint run
   gosec ./...
   ```

4. **Format Code**
   ```bash
   go fmt ./...
   go mod tidy
   ```

5. **Check for Build Errors**
   ```bash
   go build ./...
   ```

6. **Review Changes**
   - Ensure code follows project style
   - Add/update tests for new code
   - Update documentation if needed

7. **Git Workflow — Create Branch (если еще не создана)**
   ```bash
   git checkout -b fix/description  # или feature/description
   ```
   - ❌ НИКОГДА не коммить напрямую в main!

8. **Commit Changes**
   ```bash
   git add <files>
   git commit -m "fix: clear description"
   ```
   - Write clear commit messages (Conventional Commits)
   - Reference issues if applicable

9. **Push Branch**
   ```bash
   git push origin <branch-name>
   ```

10. **Create Pull Request**
    ```bash
    gh pr create --base dev --title "fix: description" --body "..."
    ```
    - **Note:** PRs must target `dev` branch (not `main`) per git-workflow guidance
    - Ждать review и одобрения
    - Только после одобрения мержить PR