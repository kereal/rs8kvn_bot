The user wants me to create a comprehensive project summary in markdown format based on the conversation history and context provided. Let me analyze what I know:

1. This is the TGVPN Go project - a Telegram bot for distributing proxy subscriptions
2. The context from QWEN.md provides detailed project information
3. The user just asked about "github mcp" but hasn't started any specific task yet
4. This appears to be a context setup session, not an active development session

Let me create a comprehensive summary based on the project context provided.# Project Summary

## Overall Goal
Maintain and develop **TGVPN Go** - a Telegram bot that distributes VLESS proxy subscriptions from a 3x-ui panel with automatic traffic limits, monthly renewal, and SQLite persistence.

## Key Knowledge

### Technology Stack
| Component | Choice |
|-----------|--------|
| Language | Go 1.21+ |
| Database | SQLite with GORM ORM |
| Telegram API | `go-telegram-bot-api/v5` |
| Logging | Uber Zap + lumberjack rotation |
| Deployment | Docker (multi-arch: amd64, arm64) |

### Architecture
- Single-binary Go application (`cmd/bot/main.go`)
- All internal packages under `internal/`
- Environment-driven configuration via `.env`
- Daily automated backups at 03:00
- Graceful shutdown with 30s timeout

### Critical Configuration
| Variable | Default | Purpose |
|----------|---------|---------|
| `TELEGRAM_BOT_TOKEN` | Required | Bot authentication |
| `XUI_HOST` | Required | 3x-ui panel URL |
| `TRAFFIC_LIMIT_GB` | 100 | Monthly traffic per user |
| `TELEGRAM_ADMIN_ID` | 0 | Admin notifications |

### Build & Run Commands
```bash
# Local development
go mod tidy
go run ./cmd/bot

# Docker deployment
docker-compose up -d
docker-compose logs -f
```

### Project Conventions
- Standard Go formatting (`gofmt`)
- Error wrapping with `fmt.Errorf("%w", err)`
- Context-aware operations for cancellation
- GORM transactions for atomic DB operations
- Token bucket rate limiting (30 burst, 5/sec refill)

## Recent Actions

| Status | Activity |
|--------|----------|
| [DONE] | Project context established via QWEN.md |
| [DONE] | Directory structure verified |
| [TODO] | User requested "github mcp" - awaiting clarification |

### Current Session Status
This is a **context initialization session**. The project infrastructure is complete and operational. No active development tasks have been started yet.

## Current Plan

| # | Task | Status |
|---|------|--------|
| 1 | Awaiting user clarification on "github mcp" request | [IN PROGRESS] |
| 2 | Potential GitHub-related tasks (repo lookup, workflow review, CI/CD analysis) | [TODO] |
| 3 | Any feature development or bug fixes | [TODO] |

### Next Steps
1. Clarify the user's "github mcp" request
2. Execute GitHub-related tasks once specified
3. Continue with any TGVPN Go development needs

---

**Project Health**: ✅ Stable - All core components documented and operational  
**Last Activity**: Context setup complete, awaiting user direction  
**Working Directory**: `/home/kereal/qwen/tgvpn_go`

---

## Summary Metadata
**Update time**: 2026-03-07T20:01:38.122Z 
