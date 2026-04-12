# Code Style and Conventions

## Naming
- Use CamelCase for exported names
- Use camelCase for private names
- Interface names should end with "er" or describe the behavior
- Test files should end with `_test.go`

## Code Organization
- Use internal packages to hide implementation details
- Group related functionality in packages
- Use interfaces for testability (see `internal/interfaces/`)
- Mock implementations in `internal/testutil/`

## Testing
- Use testify/assert and testify/require for assertions
- Use mock implementations from testutil for testing
- Test coverage target: ~51%
- Run tests with: `go test -v ./... -coverprofile=coverage.out`

## Error Handling
- Return errors as last return value
- Use meaningful error messages
- Log errors with context using zap

## Comments
- Add doc comments for exported functions and types
- Comment complex logic
- Keep comments up-to-date with code changes
