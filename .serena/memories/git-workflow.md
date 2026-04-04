## Git Workflow

- **Branch strategy**: Feature branches from `main`, never commit directly to `main`
- **Branch naming**: `feature/<description>`, `fix/<description>`
- **Commits**: Conventional commits style (feat:, fix:, refactor:, test:, docs:)
- **PRs**: Create PR for review, do NOT auto-merge
- **Current feature branch**: `feature/reliable-xui-auth` (not yet merged)
- **Remote**: `https://github.com/kereal/rs8kvn_bot.git`

### Commit conventions used
- `feat:` — new functionality
- `fix:` — bug fixes
- `refactor:` — code restructuring without behavior change
- `test:` — test additions/modifications
- `docs:` — documentation changes

### Pre-commit checklist
- `go build ./...` passes
- `go test ./... -count=1 -timeout 180s` — all packages pass
- `golangci-lint run ./...` — no issues in changed files
- No secrets/credentials in commits