# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Path traversal validation in `extra_servers.txt` parser (SEC-01)
- HTTPS enforcement for sensitive URLs (XUI_HOST, webhook URLs)
- Full security audit report: `docs/review-sf35.md`

### Changed
- Subscription proxy extra servers path validation

### Fixed
- Security: Prevent directory traversal via `SUB_EXTRA_SERVERS_FILE`

## [v2.3.0] - 2026-04-10

### Added
- Subscription plan field (free/basic/premium/vip) with `/plan` admin command
- Extra servers and headers support in subscription proxy
- Configurable trial duration and rate limiting
- In-memory referral cache with periodic sync
- Admin command rate limiting (`/send` cooldown 30s)

### Changed
- Increased broadcast delay to reduce Telegram flood risk
- Improved circuit breaker for 3x-ui client
- Enhanced error classification and recovery

### Fixed
- Trial subscription binding race condition
- Cache invalidation on subscription deletion

## [v2.2.0] - 2026-02-15

### Added
- Trial landing page (`/i/{code}`) with Happ deep-links
- QR code generation for subscription import
- Rate limiting per user (token bucket)
- Circuit breaker for 3x-ui API calls
- Daily database backups with 14-day retention
- Health check endpoints (`/healthz`, `/readyz`)

### Changed
- Migrated from custom HTTP client to telegram-bot-api/v5
- Improved graceful shutdown coordination

### Fixed
- Subscription race condition on concurrent creation
- Memory leak in pending invites cache

## [v2.1.0] - 2025-12-01

### Added
- Referral system with invite codes
- Admin broadcast functionality
- Subscription proxy endpoint (`/sub/{subID}`)
- Sentry error tracking integration

### Changed
- Switched from `mapstructure` to custom flag package for config
- Improved logging structure with Zap

## [v2.0.0] - 2025-10-01

### Added
- Initial public release
- Full 3x-ui integration with auto-login
- SQLite database with GORM
- Docker support with multi-stage builds
- Comprehensive test suite (85%+ coverage)
