package subserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessLogger_CloseWithContextCanceled(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	accessLogger, err := NewAccessLogger(logPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = accessLogger.CloseWithContext(ctx)
	// CloseWithContext must close the file even if the context is already
	// cancelled (returns context.Canceled on the slow path, or nil if the
	// async writer already finished — both are valid terminal states).
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseWithContext returned unexpected error: %v", err)
	}

	_, err = os.Stat(logPath)
	assert.NoError(t, err)
}

// Тест поведения: инвалидация подписки сбрасывает кэш всех форматов, а не
// только Xray — иначе sing-box/Clash-клиент до TTL получает старый лимит.
func TestService_InvalidateSubscriber_RemovesAllFormats(t *testing.T) {
	t.Parallel()

	svc := NewService(time.Minute)
	defer svc.Stop()

	const subID = "sub-invalidate"
	body := []byte(`[]`)
	headers := map[string]string{"content-type": "application/json"}

	for _, format := range []OutputFormat{OutputXray, OutputSingbox, OutputClash} {
		svc.Set(subscriptionCacheKey(subID, format), body, headers)
	}

	// Соседняя подписка не должна пострадать.
	svc.Set(subscriptionCacheKey("other-sub", OutputXray), body, headers)

	svc.InvalidateSubscriber(subID)

	for _, format := range []OutputFormat{OutputXray, OutputSingbox, OutputClash} {
		_, _, hit := svc.Get(subscriptionCacheKey(subID, format))
		assert.False(t, hit, "формат %s должен быть сброшен", format.String())
	}

	_, _, hit := svc.Get(subscriptionCacheKey("other-sub", OutputXray))
	assert.True(t, hit, "чужая подписка не инвалидируется")
}
