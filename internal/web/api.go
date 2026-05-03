package web

import (
	"encoding/json"
	"net/http"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// SubscriptionResponse represents a subscription in the API response.
type SubscriptionResponse struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	Enabled           bool   `json:"enabled"`
	SubscriptionToken string `json:"subscription_token"`
	Plan              string `json:"plan"`
}

// GetSubscriptions handles GET /api/v1/subscriptions.
// Returns a list of all active subscriptions.
// Requires Bearer token authentication.
func (s *Server) GetSubscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all active subscriptions from database
	subs, err := s.db.GetAllSubscriptions(r.Context())
	if err != nil {
		logger.Error("Failed to get subscriptions from database", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Filter active subscriptions and convert to response format
	result := make([]SubscriptionResponse, 0, len(subs))
	for _, sub := range subs {
		// Only include active subscriptions that are not soft-deleted
		if sub.IsActive() && !sub.DeletedAt.Valid {
			result = append(result, SubscriptionResponse{
				ID:                sub.ClientID,
				Email:             sub.Username,
				Enabled:           sub.IsActive(),
				SubscriptionToken: sub.SubscriptionID,
				Plan:              sub.Plan,
			})
		}
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, private")

	// Encode and send response
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"subscriptions": result,
	}); err != nil {
		logger.Error("Failed to encode subscriptions response", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	logger.Info("API: subscriptions list returned",
		zap.Int("count", len(result)))
}
