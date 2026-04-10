package web

import (
	"net/http"
	"strings"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// BearerAuthMiddleware returns a middleware that validates Bearer token in Authorization header.
// If the token is valid, the request is passed to the next handler.
// BearerAuthMiddleware constructs a middleware that enforces a specific Bearer token for incoming HTTP requests.
// It skips authentication for OPTIONS requests. If `expectedToken` is empty, the Authorization header is missing,
// not prefixed with "Bearer ", or does not equal `expectedToken`, the middleware responds with 401 Unauthorized and
// does not call the next handler. When the token is valid, the request is forwarded to the next handler.
func BearerAuthMiddleware(expectedToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail closed: reject all requests if expectedToken is empty/whitespace (misconfiguration)
			if strings.TrimSpace(expectedToken) == "" {
				logger.Error("BearerAuthMiddleware misconfigured: empty expectedToken",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Skip auth for OPTIONS requests (CORS preflight)
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
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
			if token != expectedToken {
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