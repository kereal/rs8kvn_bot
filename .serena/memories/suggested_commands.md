# Suggested Commands

## Development
```bash
# Run the bot
go run ./cmd/bot

# Build the bot
go build -o rs8kvn_bot ./cmd/bot

# Run tests
go test -v ./...

# Run tests with coverage
go test -v ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Run linters
golangci-lint run
gosec ./...

# Format code
go fmt ./...
```

## Docker
```bash
# Build image
docker build -t rs8kvn_bot .

# Run with docker-compose
docker-compose up -d
```

## Database
```bash
# Database is SQLite, located at data/tgvpn.db
# Backups are in data/ directory
```

## Terminal Tool Usage (Important!)

**When using terminal commands, use `cd: "tgvpn_go"` (basename), NOT absolute path!**

```bash
# ✅ CORRECT - use basename
terminal(cd="tgvpn_go", command="git status")

# ❌ WRONG - causes worktree error
terminal(cd="/home/kereal/tgvpn_go", command="git status")
```

## Git Workflow

This project uses **git-workflow-skill** (`.agents/skills/git-workflow-skill/`) for best practices.

### Conventional Commits
```bash
# Format: <type>[scope]: <description>
git commit -m "feat: add new feature"
git commit -m "fix: resolve bug in subscription"
git commit -m "docs: update README"
git commit -m "refactor: improve code structure"

# Types: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert
# Breaking change: add "!" after type or "BREAKING CHANGE:" in footer
```

### Branch Naming
```bash
feature/TICKET-123-description
fix/TICKET-456-bug-name
release/1.2.0
hotfix/1.2.1-security-patch
```

### Common Git Commands
```bash
git status
git add .
git commit -m "message"
git push

# Create branch
git checkout -b feature/TICKET-123-description

# GitHub CLI (gh)
gh pr create --title "Title" --body "Description"
gh pr list
gh pr merge <number>
gh release create v1.0.0
```
