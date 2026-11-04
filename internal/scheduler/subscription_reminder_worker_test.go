package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
)

type fakeSubSvc struct {
	bot    interfaces.BotAPI
	sendFn func(ctx context.Context, sub *database.Subscription, bit int, daysLeft int, hoursLeft int) error
}

func (f *fakeSubSvc) SendExpiryReminder(ctx context.Context, sub *database.Subscription, window interfaces.SubscriptionReminderWindow) error {
	daysLeft, hoursLeft := service.ReminderWindowRemaining(time.Now().UTC(), *sub.ExpiresAt)
	return f.sendFn(ctx, sub, window.Bit, daysLeft, hoursLeft)
}

func TestSubscriptionReminderWorker_process_SendsReminderForMatchingWindow(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sub := database.Subscription{
		ID:         1,
		TelegramID: 101,
		Status:     "active",
		ExpiresAt:  testutil.PtrTime(now.Add(72*time.Hour + 10*time.Minute)),
	}

	repo := testutil.NewSubscriptionRepositoryFake()
	repo.GetSubscriptionsExpiringInRangeFunc = func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
		if from.Sub(now) >= 71*time.Hour && from.Sub(now) <= 73*time.Hour {
			return []database.Subscription{sub}, nil
		}

		return nil, nil
	}

	var sent []struct {
		sub       *database.Subscription
		bit       int
		daysLeft  int
		hoursLeft int
	}

	svc := &fakeSubSvc{
		bot: testutil.NewBotAPI(),
		sendFn: func(ctx context.Context, s *database.Subscription, bit int, daysLeft int, hoursLeft int) error {
			sent = append(sent, struct {
				sub       *database.Subscription
				bit       int
				daysLeft  int
				hoursLeft int
			}{s, bit, daysLeft, hoursLeft})

			return nil
		},
	}

	ctx := context.Background()
	worker := NewSubscriptionReminderWorker(repo, svc)
	worker.process(ctx)

	assert.Equal(t, 0, sent[0].hoursLeft, "72h10m -> whole hours % 24 == 0")
	assert.Equal(t, service.ReminderBit3Days, sent[0].bit)
	assert.Equal(t, 3, sent[0].daysLeft)
	assert.Equal(t, 0, sent[0].hoursLeft)
}

func TestSubscriptionReminderWorker_process_SkipsSendError(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	repo := testutil.NewSubscriptionRepositoryFake()
	repo.GetSubscriptionsExpiringInRangeFunc = func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
		if from.Sub(now) >= 23*time.Hour && from.Sub(now) <= 25*time.Hour {
			return []database.Subscription{{
				ID:         2,
				TelegramID: 202,
				Status:     "active",
				ExpiresAt:  testutil.PtrTime(now.Add(24*time.Hour - 5*time.Minute)),
			}}, nil
		}

		return nil, nil
	}

	callCount := 0
	svc := &fakeSubSvc{
		bot: testutil.NewBotAPI(),
		sendFn: func(ctx context.Context, sub *database.Subscription, bit int, daysLeft int, hoursLeft int) error {
			callCount++
			return errors.New("telegram down")
		},
	}

	ctx := context.Background()
	worker := NewSubscriptionReminderWorker(repo, svc)
	worker.process(ctx)

	assert.Equal(t, 1, callCount, "worker should attempt once per window despite send error")
}

// Табличный прогон воркера по всем окнам напоминаний через реальный
// SubscriptionService: claim=true → ровно одна отправка и заявленный бит окна.
func TestSubscriptionReminderWorker_process_ReminderWindows(t *testing.T) {
	t.Parallel()

	for _, window := range service.ExpiryReminderWindows() {
		t.Run(window.Name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()
			sub := database.Subscription{
				ID:         1,
				TelegramID: 301,
				Status:     "active",
				ExpiresAt:  testutil.PtrTime(now.Add(window.LeadTime + 10*time.Minute)),
			}

			repo := testutil.NewSubscriptionRepositoryFake()
			repo.GetSubscriptionsExpiringInRangeFunc = func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
				// Возврат подписи только в окне текущего скана (worker
				// передаёт from/to = центр ± 30 минут).
				if from.Sub(now) >= window.LeadTime-30*time.Minute && from.Sub(now) <= window.LeadTime+30*time.Minute {
					return []database.Subscription{sub}, nil
				}

				return nil, nil
			}

			var claimedBit int
			repo.ClaimReminderFunc = func(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
				claimedBit = bit

				return true, nil
			}

			bot := testutil.NewBotAPI()
			subService := service.NewSubscriptionService(repo, nil, nil, &config.Config{})
			subService.SetBot(bot)

			NewSubscriptionReminderWorker(repo, subService).process(context.Background())

			assert.Equal(t, 1, bot.SendCount, "matching window sends exactly one reminder")
			assert.Equal(t, window.Bit, claimedBit, "claimed bit must match the window")
		})
	}
}

// Claim=false (уже отправлено на прошлом прогоне) → тихий скип, бот молчит.
func TestSubscriptionReminderWorker_process_ClaimFalseSkips(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sub := database.Subscription{
		ID:         1,
		TelegramID: 302,
		Status:     "active",
		ExpiresAt:  testutil.PtrTime(now.Add(72*time.Hour + 10*time.Minute)),
	}

	repo := testutil.NewSubscriptionRepositoryFake()
	repo.GetSubscriptionsExpiringInRangeFunc = func(ctx context.Context, from, to time.Time) ([]database.Subscription, error) {
		if from.Sub(now) >= 71*time.Hour && from.Sub(now) <= 73*time.Hour {
			return []database.Subscription{sub}, nil
		}

		return nil, nil
	}
	repo.ClaimReminderFunc = func(context.Context, uint, int, time.Time) (bool, error) {
		return false, nil
	}

	bot := testutil.NewBotAPI()
	subService := service.NewSubscriptionService(repo, nil, nil, &config.Config{})
	subService.SetBot(bot)

	NewSubscriptionReminderWorker(repo, subService).process(context.Background())

	assert.Zero(t, bot.SendCount, "unclaimed reminder must not be sent")
}
