package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// ResetTraffic обнуляет счётчик трафика (up/down) клиента на панели 3x-ui.
// Используется при смене тарифа, чтобы трафик предыдущего периода не
// влиял на лимит нового тарифа.
func (c *Client) ResetTraffic(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("reset traffic: email cannot be empty")
	}

	return utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doResetTraffic(ctx, email)
	})
}

// doResetTraffic выполняет POST /panel/api/clients/resetTraffic/{email}.
func (c *Client) doResetTraffic(ctx context.Context, email string) error {
	resetURL := fmt.Sprintf("%s/panel/api/clients/resetTraffic/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, resetURL, nil)
	if err != nil {
		return err
	}

	var apiResp APIResponse

	err = json.Unmarshal(respBody, &apiResp)
	if err != nil {
		return fmt.Errorf("failed to parse resetTraffic response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("failed to reset traffic: %s", apiResp.Msg)
	}

	logger.Info("Successfully reset client traffic",
		zap.String("email", email))

	return nil
}
