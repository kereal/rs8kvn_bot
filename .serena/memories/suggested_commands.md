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

## Git
```bash
# Standard git commands
git status
git add .
git commit -m "message"
git push
```
