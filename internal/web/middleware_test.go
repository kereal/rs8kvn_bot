package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearerAuthMiddleware_ValidToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("my-secret-token")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer my-secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "next handler should be called")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBearerAuthMiddleware_InvalidToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("correct-token")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.False(t, called, "next handler should not be called")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
}

func TestBearerAuthMiddleware_MissingHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("my-token")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.False(t, called, "next handler should not be called")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
}

func TestBearerAuthMiddleware_EmptyHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("my-token")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.False(t, called, "next handler should not be called")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBearerAuthMiddleware_WrongScheme(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"Basic auth", "Basic dXNlcjpwYXNz"},
		{"Token without Bearer prefix", "my-secret-token"},
		{"Bearer lowercase", "bearer my-secret-token"},
		{"Double Bearer", "Bearer Bearer my-secret-token"},
		{"Bearer with extra space", "Bearer  my-secret-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := BearerAuthMiddleware("my-secret-token")(next)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
			req.Header.Set("Authorization", tt.header)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.False(t, called, "next handler should not be called")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestBearerAuthMiddleware_OptionsRequest(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("my-token")(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/subscriptions", nil)
	// No Authorization header for OPTIONS
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "next handler should be called for OPTIONS without auth")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBearerAuthMiddleware_EmptyExpectedToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware("")(next)

	tests := []struct {
		name        string
		authHeader  string
		shouldAllow bool
	}{
		{"empty bearer token", "Bearer ", false},
		{"no auth header", "", false},
		{"non-empty token", "Bearer something", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.shouldAllow, called)
			if tt.shouldAllow {
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
			}
		})
	}
}

func TestBearerAuthMiddleware_TokenWithSpecialChars(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		header string
	}{
		{"UUID token", "550e8400-e29b-41d4-a716-446655440000", "Bearer 550e8400-e29b-41d4-a716-446655440000"},
		{"Base64 token", "dGVzdF90b2tlbg==", "Bearer dGVzdF90b2tlbg=="},
		{"JWT-like token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0"},
		{"Token with dots", "my.token.value", "Bearer my.token.value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := BearerAuthMiddleware(tt.token)(next)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
			req.Header.Set("Authorization", tt.header)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.True(t, called, "next handler should be called with matching token")
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

func TestBearerAuthMiddleware_DifferentMethods(t *testing.T) {
	token := "test-token"
	handler := BearerAuthMiddleware(token)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/subscriptions", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

func TestBearerAuthMiddleware_MultipleRequests(t *testing.T) {
	token := "shared-token"
	callCount := 0

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware(token)(next)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	}

	assert.Equal(t, 5, callCount)
}

func TestBearerAuthMiddleware_RejectThenAllow(t *testing.T) {
	token := "secret"
	callCount := 0

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	handler := BearerAuthMiddleware(token)(next)

	// First request with wrong token
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, callCount)

	// Second request with correct token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, callCount)
}