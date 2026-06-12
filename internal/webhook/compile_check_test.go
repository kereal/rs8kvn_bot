package webhook_test

import (
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/webhook"
)

func TestCompileTimeInterfaceChecks(t *testing.T) {
	var _ webhook.WebhookSender = (*webhook.Sender)(nil)
	var _ webhook.WebhookSender = (*webhook.NoopSender)(nil)
}
