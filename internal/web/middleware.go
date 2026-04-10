package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// BearerAuthMiddleware returns a middleware that validates Bearer token in Authorization header.
// If the token is valid, the request is passed to the next handler.
// If the token is invalid or missing, a 401 Unauthorized response is returned.
func BearerAuthMiddleware(expectedToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for OPTIONS requests (CORS preflight)
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// If no expected token is configured, reject all requests.
			// An empty token is an invalid configuration — allowing access
			// with an empty Bearer token would be a security hole.
			if expectedToken == "" {
				logger.Warn("No auth token configured, rejecting request",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Get Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				logger.Warn("Missing Authorization header",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Check Bearer prefix
			if !strings.HasPrefix(authHeader, "Bearer ") {
				logger.Warn("Invalid Authorization header format",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Extract token
			token := strings.TrimPrefix(authHeader, "Bearer ")
			// Use constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
				logger.Warn("Invalid Bearer token",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Token is valid, proceed to next handler
			next.ServeHTTP(w, r)
		})
	}
}
