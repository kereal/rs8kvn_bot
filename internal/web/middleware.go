package web

import (
	"net/http"
	"strings"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// BearerAuthMiddleware returns a middleware that validates Bearer token in Authorization header.
// If the token is valid, the request is passed to the next handler.
// BearerAuthMiddleware returns an HTTP middleware that enforces a Bearer token equal to expectedToken.
// It bypasses authentication for OPTIONS requests. If the Authorization header is missing, does not start
// with "Bearer ", or the extracted token does not match expectedToken, the middleware logs a warning with
// the request path and method and responds with HTTP 401 and body "unauthorized". On a valid token it calls
// the next handler.
func BearerAuthMiddleware(expectedToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
