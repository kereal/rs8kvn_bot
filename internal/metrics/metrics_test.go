package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty path", "", ""},
		{"root", "/", "/"},
		{"static health", "/healthz", "/healthz"},
		{"static ready", "/readyz", "/readyz"},
		{"static api", "/api/v1/subscriptions", "/api/v1/subscriptions"},
		{"invite with code", "/i/abc12345", "/i/:code"},
		{"invite with long code", "/i/abcdef1234567890", "/i/:code"},
		{"invite with subpath", "/i/abc/sub", "/i/:code"},
		{"subscription id", "/sub/abc-123-xyz", "/sub/:id"},
		{"subscription uuid", "/sub/550e8400-e29b-41d4-a716-446655440000", "/sub/:id"},
		{"static after slash", "/static/logo.png", "/static/logo.png"},
		{"mixed static", "/api/v1/users/123", "/api/v1/users/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizePath(tt.in))
		})
	}
}

func TestStatusCodeString(t *testing.T) {
	t.Parallel()

	rr := &responseRecorder{statusCode: 200}
	assert.Equal(t, "OK", rr.statusCodeString())

	rr.statusCode = 404
	assert.Equal(t, "Not Found", rr.statusCodeString())

	rr.statusCode = 500
	assert.Equal(t, "Internal Server Error", rr.statusCodeString())
}
