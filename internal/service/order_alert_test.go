package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatPaymentIssue_TruncatesUntrustedFields(t *testing.T) {
	message := formatPaymentIssue(PaymentIssue{Event: "event", Reason: strings.Repeat("x", 5000), Payload: strings.Repeat("y", 5000)})

	assert.LessOrEqual(t, len(message), 5000)
	assert.Contains(t, message, "[truncated]")
}

func TestNotifyPaidUser_MissingSubscriptionServiceReturnsError(t *testing.T) {
	providerID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440211")
	db := &testutil.DatabaseService{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, TelegramID: 42}, nil
		},
	}
	o := NewOrderService(db, nil, nil, fakePaymentProvider{}, "", &config.Config{})

	_, _, err := o.NotifyPaidUser(context.Background(), &database.Order{ID: 1, SubscriptionID: 1, ProviderPaymentID: providerID.String()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription service is not configured")
}
