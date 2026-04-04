package service

import (
	"context"
	"fmt"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/xui"

	"rs8kvn_bot/internal/utils"
)

type SubscriptionService struct {
	db  interfaces.DatabaseService
	xui interfaces.XUIClient
	cfg *config.Config
}

type CreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
}

func NewSubscriptionService(db interfaces.DatabaseService, xui interfaces.XUIClient, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{
		db:  db,
		xui: xui,
		cfg: cfg,
	}
}

func (s *SubscriptionService) Create(ctx context.Context, chatID int64, username string) (*CreateResult, error) {
	trafficBytes := int64(s.cfg.TrafficLimitGB) * 1024 * 1024 * 1024

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	// Calculate expiry time for auto-reset (now + reset days)
	expiryTime := time.Now().Add(time.Duration(config.SubscriptionResetDay) * 24 * time.Hour)

	client, err := s.xui.AddClientWithID(ctx, s.cfg.XUIInboundID, username, clientID, subID, trafficBytes, expiryTime, config.SubscriptionResetDay)
	if err != nil {
		return nil, fmt.Errorf("xui add client: %w", err)
	}

	subscriptionURL := s.xui.GetSubscriptionLink(s.xui.GetExternalURL(s.cfg.XUIHost), client.SubID, s.cfg.XUISubPath)

	sub := &database.Subscription{
		TelegramID:      chatID,
		Username:        username,
		ClientID:        client.ID,
		SubscriptionID:  client.SubID,
		InboundID:       s.cfg.XUIInboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      time.Time{},
		Status:          "active",
		SubscriptionURL: subscriptionURL,
	}

	if err := s.db.CreateSubscription(ctx, sub); err != nil {
		// Retry rollback with backoff to ensure client is deleted from XUI
		rollbackErr := xui.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, client.ID)
		})
		if rollbackErr != nil {
			return nil, fmt.Errorf("create subscription: %w (rollback failed: %w)", err, rollbackErr)
		}
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	return &CreateResult{
		Subscription:    sub,
		SubscriptionURL: subscriptionURL,
	}, nil
}

func (s *SubscriptionService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	return s.db.GetByTelegramID(ctx, telegramID)
}

func (s *SubscriptionService) Delete(ctx context.Context, telegramID int64) error {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return err
	}

	if err := s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, sub.ClientID); err != nil {
		return fmt.Errorf("xui delete: %w", err)
	}

	if err := s.db.DeleteSubscription(ctx, telegramID); err != nil {
		return fmt.Errorf("db delete: %w", err)
	}

	return nil
}

type TrafficInfo struct {
	UsedGB              float64
	LimitGB             int
	Percentage          float64
	ProgressBar         string
	DaysUntilReset      int
	ResetInfo           string
	CreatedAtFormatted  string
	ExpiryTimeFormatted string
}

func (s *SubscriptionService) GetWithTraffic(ctx context.Context, telegramID int64) (*database.Subscription, *TrafficInfo, error) {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, nil, err
	}

	// Get traffic from XUI
	traffic, err := s.xui.GetClientTraffic(ctx, sub.Username)
	if err != nil {
		// Return subscription with zero traffic instead of failing
		return sub, &TrafficInfo{
			UsedGB:  0,
			LimitGB: s.cfg.TrafficLimitGB,
		}, nil
	}

	usedGB := float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
	percentage := 0.0
	limitGB := float64(s.cfg.TrafficLimitGB)
	if limitGB > 0 {
		percentage = (usedGB / limitGB) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Progress bar
	progressBar := generateProgressBar(usedGB, limitGB)

	// Calculate reset time
	var resetTime time.Time
	if sub.ExpiryTime.IsZero() {
		resetTime = sub.CreatedAt.AddDate(0, 0, config.SubscriptionResetDay)
	} else {
		resetTime = sub.ExpiryTime
	}
	daysUntilReset := daysUntilReset(time.Now(), resetTime)

	// Reset info string
	var resetInfo string
	if daysUntilReset < 0 {
		resetInfo = "🔄 Сброс: отключен"
	} else if daysUntilReset == 0 {
		resetInfo = "🔄 Сброс: сегодня"
	} else {
		resetInfo = fmt.Sprintf("🔄 Сброс: через %d дн.", daysUntilReset)
	}

	return sub, &TrafficInfo{
		UsedGB:              usedGB,
		LimitGB:             s.cfg.TrafficLimitGB,
		Percentage:          percentage,
		ProgressBar:         progressBar,
		DaysUntilReset:      daysUntilReset,
		ResetInfo:           resetInfo,
		CreatedAtFormatted:  formatDateRu(sub.CreatedAt),
		ExpiryTimeFormatted: formatDateRu(sub.ExpiryTime),
	}, nil
}

// daysUntilReset calculates the number of days until the next traffic reset.
// Returns -1 if auto-reset is not configured (expiryTime is zero).
// Returns 0 if already expired (reset should happen now).
// Returns positive number of days until reset otherwise.
func daysUntilReset(now time.Time, expiryTime time.Time) int {
	if expiryTime.IsZero() {
		return -1 // Auto-reset not configured
	}

	if now.After(expiryTime) || now.Equal(expiryTime) {
		return 0 // Already expired, reset should happen now
	}

	duration := expiryTime.Sub(now)
	days := int(duration.Hours() / 24)

	if days < 0 {
		days = 0
	}

	return days
}

// formatDateRu formats a date in Russian locale (e.g., "15 января 2025").
func formatDateRu(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	months := []string{
		"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}

	day := t.Day()
	month := months[t.Month()-1]
	year := t.Year()

	return fmt.Sprintf("%d %s %d", day, month, year)
}

// generateProgressBar creates a visual progress bar using Unicode emojis.
func generateProgressBar(usedGB, limitGB float64) string {
	if limitGB <= 0 {
		return "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜"
	}

	percentage := (usedGB / limitGB) * 100
	if percentage > 100 {
		percentage = 100
	}

	// 10 blocks total
	filled := int(percentage / 10)
	if filled > 10 {
		filled = 10
	}

	bar := ""
	for i := 0; i < 10; i++ {
		if i < filled {
			bar += "🟩"
		} else {
			bar += "⬜"
		}
	}

	return bar
}

type TrialCreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	SubID           string
	ClientID        string
}

func (s *SubscriptionService) CreateTrial(ctx context.Context, inviteCode string) (*TrialCreateResult, error) {
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	trafficBytes := calcTrialTraffic(s.cfg.TrialDurationHours)
	expiryTime := time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)

	_, err = s.xui.AddClientWithID(ctx, s.cfg.XUIInboundID, "trial_"+subID, clientID, subID, trafficBytes, expiryTime, 0)
	if err != nil {
		return nil, fmt.Errorf("xui add client: %w", err)
	}

	subURL := s.xui.GetSubscriptionLink(s.xui.GetExternalURL(s.cfg.XUIHost), subID, s.cfg.XUISubPath)

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, s.cfg.XUIInboundID, trafficBytes, expiryTime, subURL)
	if err != nil {
		if rollbackErr := s.xui.DeleteClient(ctx, s.cfg.XUIInboundID, clientID); rollbackErr != nil {
			return nil, fmt.Errorf("create trial subscription: %w (rollback failed: %w)", err, rollbackErr)
		}
		return nil, fmt.Errorf("create trial subscription: %w", err)
	}

	return &TrialCreateResult{
		Subscription:    sub,
		SubscriptionURL: subURL,
		SubID:           subID,
		ClientID:        clientID,
	}, nil
}

// calcTrialTraffic calculates trial traffic allocation based on trial duration.
// Formula: trialHours * 1GiB / 12, where 12 = (24*365)/(30*24) = hours in year / hours in month.
// This gives a proportional share of monthly traffic (100 GiB). Minimum 1 GiB.
func calcTrialTraffic(trialHours int) int64 {
	const (
		gib        = 1024 * 1024 * 1024
		minTraffic = gib
		// hoursInYear / hoursInMonth ≈ 12.17, integer division gives 12
		trafficDivisor = (24 * 365) / (30 * 24)
	)

	trafficBytes := int64(trialHours) * gib / trafficDivisor
	if trafficBytes < minTraffic {
		trafficBytes = minTraffic
	}
	return trafficBytes
}
