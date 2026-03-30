# Task Completion Checklist

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

7. **Commit Changes**
   - Write clear commit messages
   - Reference issues if applicable
