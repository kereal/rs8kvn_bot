package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// ReminderBit3Days marks the 3-day reminder.
	ReminderBit3Days = 1 << iota
	// ReminderBit1Day marks the 1-day reminder.
	ReminderBit1Day
	// ReminderBit3Hours marks the 3-hour reminder.
	ReminderBit3Hours
)

var expiryReminderWindows = [3]interfaces.SubscriptionReminderWindow{
	{Name: "3d", Bit: ReminderBit3Days, LeadTime: 72 * time.Hour},
	{Name: "1d", Bit: ReminderBit1Day, LeadTime: 24 * time.Hour},
	{Name: "3h", Bit: ReminderBit3Hours, LeadTime: 3 * time.Hour},
}

// ExpiryReminderWindows returns the configured reminder windows by value.
func ExpiryReminderWindows() [3]interfaces.SubscriptionReminderWindow {
	return expiryReminderWindows
}

// ReminderWindowBounds returns the scan range for a worker tick.
func ReminderWindowBounds(w interfaces.SubscriptionReminderWindow, now time.Time) (time.Time, time.Time) {
	const halfTick = 30 * time.Minute
	center := now.Add(w.LeadTime)
	return center.Add(-halfTick), center.Add(halfTick)
}

// ReminderWindowRemaining converts time left into values shown to users.
func ReminderWindowRemaining(now, expiresAt time.Time) (daysLeft, hoursLeft int) {
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0, 0
	}
	return int(remaining.Hours() / 24), int(remaining.Hours()) % 24
}

// SendExpiryReminder claims a reminder bit before sending and releases it on send failure.
func (s *SubscriptionService) SendExpiryReminder(ctx context.Context, sub *database.Subscription, window interfaces.SubscriptionReminderWindow) error {
	if s.bot == nil || sub == nil || sub.TelegramID == 0 || sub.ExpiresAt == nil {
		return nil
	}

	claimed, err := s.reminderRepo.ClaimReminder(ctx, sub.ID, window.Bit, *sub.ExpiresAt)
	if err != nil {
		metrics.SubscriptionRemindersTotal.WithLabelValues(window.Name, "error").Inc()
		return fmt.Errorf("claim reminder: %w", err)
	}
	if !claimed {
		return nil
	}

	daysLeft, hoursLeft := ReminderWindowRemaining(time.Now().UTC(), *sub.ExpiresAt)
	text := utils.EscapeMarkdownV2(reminderText(daysLeft, hoursLeft, s.cfg.SubURL(sub.SubscriptionID)))
	msg := tgbotapi.NewMessage(sub.TelegramID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	if _, err := s.bot.Send(msg); err != nil {
		if releaseErr := s.reminderRepo.ReleaseReminder(ctx, sub.ID, window.Bit, *sub.ExpiresAt); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
		metrics.SubscriptionRemindersTotal.WithLabelValues(window.Name, "error").Inc()
		return fmt.Errorf("send reminder: %w", err)
	}

	metrics.SubscriptionRemindersTotal.WithLabelValues(window.Name, "success").Inc()
	return nil
}

func reminderText(daysLeft int, hoursLeft int, subURL string) string {
	const renewalInstruction = "\n\nЧтобы продлить подписку, откройте главное меню — нажмите /start."
	if daysLeft > 0 {
		return fmt.Sprintf("⏳ До окончания подписки осталось %d д\n%s%s", daysLeft, subURL, renewalInstruction)
	}
	return fmt.Sprintf("🚨 До окончания подписки осталось %d ч\n%s%s", hoursLeft, subURL, renewalInstruction)
}
