package bot

// This worker owns durable campaign execution: it claims recipients, records
// every terminal outcome, and leaves retry metadata in broadcasts on failures.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

const (
	broadcastWorkerInterval = 15 * time.Second
	broadcastTimeout        = 5 * time.Minute
	broadcastRetryBaseDelay = 5 * time.Second
	broadcastRetryMaxDelay  = 15 * time.Minute
	broadcastConcurrency    = 10
	broadcastBatchSize      = 100
)

// BroadcastWorker processes durable broadcast campaigns independently from the
// Telegram update loop. A process restart resumes scheduled/running campaigns.
type BroadcastWorker struct {
	h        *Handler
	mu       sync.Mutex
	activeMu sync.Mutex
	active   map[uint]context.CancelFunc
}

func NewBroadcastWorker(h *Handler) *BroadcastWorker {
	return &BroadcastWorker{h: h, active: make(map[uint]context.CancelFunc)}
}

func (w *BroadcastWorker) Run(ctx context.Context) {
	logger.Info("Broadcast worker started")
	w.process(ctx)
	ticker := time.NewTicker(broadcastWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.process(ctx)
		case <-ctx.Done():
			logger.Info("Broadcast worker stopped")
			return
		}
	}
}

func (w *BroadcastWorker) process(ctx context.Context) {
	campaigns, err := w.h.db.GetRunnableBroadcasts(ctx, time.Now().UTC())
	if err != nil {
		logger.Warn("Failed to load runnable broadcasts", zap.Error(err))
		return
	}
	for i := range campaigns {
		if err := w.processCampaign(ctx, &campaigns[i]); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("Broadcast campaign failed", zap.Uint("broadcast_id", campaigns[i].ID), zap.Error(err))
		}
	}
}

func (w *BroadcastWorker) processCampaign(ctx context.Context, campaign *database.Broadcast) (resultErr error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Keep retry persistence inside the same lock as campaign processing. This
	// prevents another worker pass from claiming the campaign between failure
	// and writing retry_at.
	defer func() {
		if resultErr == nil || errors.Is(resultErr, context.Canceled) {
			return
		}
		if retryErr := w.scheduleRetry(context.WithoutCancel(ctx), campaign.ID, resultErr); retryErr != nil {
			logger.Error("Failed to schedule broadcast retry", zap.Uint("broadcast_id", campaign.ID), zap.Error(retryErr))
		}
	}()

	campaignCtx, cancel := context.WithTimeout(ctx, broadcastTimeout)
	w.activeMu.Lock()
	w.active[campaign.ID] = cancel
	w.activeMu.Unlock()
	defer func() {
		cancel()
		w.activeMu.Lock()
		delete(w.active, campaign.ID)
		w.activeMu.Unlock()
	}()

	fresh, err := w.h.db.GetBroadcast(campaignCtx, campaign.ID)
	if err != nil {
		return fmt.Errorf("load broadcast before processing: %w", err)
	}
	if fresh.Status == string(database.BroadcastStatusCompleted) || fresh.Status == string(database.BroadcastStatusFailed) || fresh.Status == string(database.BroadcastStatusCanceled) {
		return nil
	}
	campaign = fresh

	if campaign.Status == string(database.BroadcastStatusScheduled) {
		claimed, err := w.h.db.ClaimBroadcast(campaignCtx, campaign.ID, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("claim broadcast: %w", err)
		}
		if !claimed {
			return nil
		}
	}

	// Snapshot is idempotent and also covers a crash between claim and snapshot.
	filter, err := database.ParseBroadcastFilter(campaign.Filters)
	if err != nil {
		parseErr := fmt.Errorf("parse broadcast filter: %w", err)
		if markErr := w.markCampaignFailed(campaignCtx, campaign.ID, parseErr); markErr != nil {
			return errors.Join(parseErr, fmt.Errorf("mark campaign failed: %w", markErr))
		}
		return parseErr
	}
	total, err := w.h.db.SnapshotBroadcastRecipients(campaignCtx, campaign.ID, filter)
	if err != nil {
		return fmt.Errorf("snapshot broadcast recipients: %w", err)
	}
	campaign.RecipientsTotal = total

	if err := w.h.db.RecoverStaleBroadcastRecipients(campaignCtx, campaign.ID, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		return err
	}

	var current *database.Broadcast
	for {
		if campaignCtx.Err() != nil {
			break
		}
		current, err = w.h.db.GetBroadcast(campaignCtx, campaign.ID)
		if err != nil {
			if campaignCtx.Err() != nil {
				break
			}
			return fmt.Errorf("check broadcast status: %w", err)
		}
		if current.Status == string(database.BroadcastStatusCanceled) {
			campaign = current
			break
		}
		recipients, err := w.h.db.ClaimBroadcastRecipients(campaignCtx, campaign.ID, time.Now().UTC(), broadcastBatchSize)
		if err != nil {
			if campaignCtx.Err() != nil {
				break
			}
			return err
		}
		if len(recipients) == 0 {
			break
		}
		if err := w.processRecipients(campaignCtx, campaign.MessageText, recipients); err != nil && campaignCtx.Err() == nil {
			return fmt.Errorf("process broadcast recipients: %w", err)
		}
	}

	finishCtx := campaignCtx
	if campaignCtx.Err() != nil {
		finishCtx = context.WithoutCancel(campaignCtx)
	}
	current, err = w.h.db.GetBroadcast(finishCtx, campaign.ID)
	if err != nil {
		return fmt.Errorf("load broadcast for finalization: %w", err)
	}
	// A process shutdown or worker timeout must leave a still-running campaign
	// resumable. Only an explicit admin cancellation is finalized here.
	if campaignCtx.Err() != nil && current.Status != string(database.BroadcastStatusCanceled) {
		return campaignCtx.Err()
	}
	campaign = current
	err = w.finishCampaign(finishCtx, campaign)
	if err == nil {
		w.sendAdminReport(finishCtx, campaign)
	}
	return err
}

func (w *BroadcastWorker) processRecipients(ctx context.Context, text string, recipients []database.BroadcastRecipient) error {
	sem := make(chan struct{}, broadcastConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
launch:
	for _, recipient := range recipients {
		select {
		case sem <- struct{}{}:
			wg.Add(1)
			go func(r database.BroadcastRecipient) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := w.processRecipientSafely(ctx, text, r); err != nil && !errors.Is(err, database.ErrBroadcastRecipientStale) {
					mu.Lock()
					errs = append(errs, fmt.Errorf("recipient %d: %w", r.ID, err))
					mu.Unlock()
				}
			}(recipient)
		case <-ctx.Done():
			break launch
		}
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (w *BroadcastWorker) processRecipientSafely(ctx context.Context, text string, recipient database.BroadcastRecipient) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("broadcast recipient panic: %v", recovered)
			finishErr := w.h.db.FinishBroadcastRecipient(context.WithoutCancel(ctx), recipient.BroadcastID, recipient.ID, recipient.Attempts, database.BroadcastRecipientFailed, panicErr.Error(), time.Now().UTC())
			if finishErr != nil {
				returnErr := fmt.Errorf("record panicked recipient: %w", finishErr)
				if errors.Is(finishErr, database.ErrBroadcastRecipientStale) {
					returnErr = finishErr
				}
				err = returnErr
			} else {
				err = panicErr
			}
			logger.Error("Broadcast recipient panicked", zap.Uint("recipient_id", recipient.ID), zap.Any("panic", recovered))
		}
	}()
	return w.processRecipient(ctx, text, recipient)
}

func (w *BroadcastWorker) processRecipient(ctx context.Context, text string, recipient database.BroadcastRecipient) error {
	chunks := splitMessage(text, config.MaxTelegramMessageLen)
	var lastErr error
	for _, chunk := range chunks {
		msg := tgbotapi.NewMessage(recipient.TelegramID, utils.EscapeMarkdownV2(chunk))
		msg.ParseMode = "MarkdownV2"
		msg.DisableWebPagePreview = true
		for attempt := 0; attempt <= broadcastRetries; attempt++ {
			err := w.h.sendWithError(ctx, msg)
			if err == nil {
				lastErr = nil
				break
			}
			if isUserBlockedError(err) {
				if finishErr := w.h.db.FinishBroadcastRecipient(context.WithoutCancel(ctx), recipient.BroadcastID, recipient.ID, recipient.Attempts, database.BroadcastRecipientBlocked, err.Error(), time.Now().UTC()); finishErr != nil {
					return fmt.Errorf("record blocked recipient: %w", finishErr)
				}
				return nil
			}
			if isUserUnreachableError(err) {
				if finishErr := w.h.db.FinishBroadcastRecipient(context.WithoutCancel(ctx), recipient.BroadcastID, recipient.ID, recipient.Attempts, database.BroadcastRecipientUnreachable, err.Error(), time.Now().UTC()); finishErr != nil {
					return fmt.Errorf("record unreachable recipient: %w", finishErr)
				}
				return nil
			}
			lastErr = err
			if attempt < broadcastRetries {
				select {
				case <-time.After(broadcastSendRetryBaseDelay * time.Duration(attempt+1)):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if lastErr != nil {
			if finishErr := w.h.db.FinishBroadcastRecipient(context.WithoutCancel(ctx), recipient.BroadcastID, recipient.ID, recipient.Attempts, database.BroadcastRecipientFailed, truncateRunes(lastErr.Error(), broadcastErrorTextMaxLen), time.Now().UTC()); finishErr != nil {
				return fmt.Errorf("record failed recipient: %w", finishErr)
			}
			return nil
		}
	}
	if err := w.h.db.FinishBroadcastRecipient(context.WithoutCancel(ctx), recipient.BroadcastID, recipient.ID, recipient.Attempts, database.BroadcastRecipientSent, "", time.Now().UTC()); err != nil {
		return fmt.Errorf("record sent recipient: %w", err)
	}
	return nil
}

func (w *BroadcastWorker) markCampaignFailed(ctx context.Context, id uint, cause error) error {
	finishedAt := time.Now().UTC()
	report := database.BroadcastDeliveryReport{Delivered: []int64{}, Blocked: []int64{}, Errors: []database.BroadcastSendError{}, NotProcessed: []int64{}}
	broadcast := &database.Broadcast{ID: id, Status: string(database.BroadcastStatusFailed), FinishedAt: &finishedAt, LastError: truncateRunes(cause.Error(), broadcastErrorTextMaxLen)}
	if err := broadcast.SetDeliveryReport(&report); err != nil {
		return err
	}
	if err := w.h.db.UpdateBroadcast(ctx, broadcast); err != nil {
		return err
	}
	logger.Warn("Broadcast marked failed", zap.Uint("broadcast_id", id), zap.Error(cause))
	return nil
}

func (w *BroadcastWorker) finishCampaign(ctx context.Context, campaign *database.Broadcast) error {
	total, sent, blocked, unreachable, failed, report, err := w.h.db.GetBroadcastRecipientsStats(ctx, campaign.ID)
	if err != nil {
		return err
	}
	terminal := sent + blocked + unreachable + failed
	status := database.BroadcastStatusCompleted
	finishedAt := time.Now().UTC()
	lastError, retryAt, retryCount := "", (*time.Time)(nil), 0
	var incompleteErr error
	if campaign.Status == string(database.BroadcastStatusCanceled) {
		status = database.BroadcastStatusCanceled
	} else if total > terminal {
		// A sending lease can survive a persistence failure until it expires.
		// Keep the campaign resumable and let process() schedule another pass.
		status = database.BroadcastStatusRunning
		finishedAt = time.Time{}
		lastError, retryAt, retryCount = campaign.LastError, campaign.RetryAt, campaign.RetryCount
		incompleteErr = fmt.Errorf("broadcast has %d unfinished recipients", total-terminal)
	}
	b := &database.Broadcast{ID: campaign.ID, Status: string(status), RecipientsTotal: total, SentCount: sent, BlockedCount: blocked, UnreachableCount: unreachable, FailedCount: failed, LastError: lastError, RetryAt: retryAt, RetryCount: retryCount}
	if !finishedAt.IsZero() {
		b.FinishedAt = &finishedAt
	}
	if err := b.SetDeliveryReport(&report); err != nil {
		return fmt.Errorf("marshal broadcast report: %w", err)
	}
	if err := w.h.db.UpdateBroadcast(ctx, b); err != nil {
		return err
	}
	return incompleteErr
}

// scheduleRetry stores an exponential delay for infrastructure or persistence
// failures so a broken campaign is retried without a tight polling loop.
func (w *BroadcastWorker) scheduleRetry(ctx context.Context, id uint, cause error) error {
	campaign, err := w.h.db.GetBroadcast(ctx, id)
	if err != nil {
		return fmt.Errorf("load broadcast retry state: %w", err)
	}
	if campaign.Status == string(database.BroadcastStatusCompleted) || campaign.Status == string(database.BroadcastStatusFailed) || campaign.Status == string(database.BroadcastStatusCanceled) {
		return nil
	}
	attempt := campaign.RetryCount
	if attempt < 0 {
		attempt = 0
	}
	delay := broadcastRetryBaseDelay
	for i := 0; i < attempt && delay < broadcastRetryMaxDelay; i++ {
		delay *= 2
	}
	if delay > broadcastRetryMaxDelay {
		delay = broadcastRetryMaxDelay
	}
	retryAt := time.Now().UTC().Add(delay)
	campaign.LastError = truncateRunes(cause.Error(), broadcastErrorTextMaxLen)
	campaign.RetryAt = &retryAt
	campaign.RetryCount = attempt + 1
	if campaign.Status == string(database.BroadcastStatusScheduled) {
		campaign.Status = string(database.BroadcastStatusRunning)
	}
	if err := w.h.db.UpdateBroadcast(ctx, campaign); err != nil {
		return fmt.Errorf("save broadcast retry state: %w", err)
	}
	return nil
}

func (w *BroadcastWorker) sendAdminReport(ctx context.Context, campaign *database.Broadcast) {
	if w.h.cfg.TelegramAdminID == 0 {
		return
	}
	_, sent, blocked, unreachable, failed, _, err := w.h.db.GetBroadcastRecipientsStats(ctx, campaign.ID)
	if err != nil {
		logger.Warn("Failed to load broadcast report", zap.Uint("broadcast_id", campaign.ID), zap.Error(err))
		return
	}
	statusText := "✅ Рассылка завершена!"
	if campaign.Status == string(database.BroadcastStatusCanceled) {
		statusText = "⚠️ Рассылка отменена"
	}
	text := fmt.Sprintf("%s\n\n📦 Рассылка #%d: %s\n\n📤 Отправлено: %d\n🚫 Заблокировали бота: %d\n⚠️ Недоступны: %d\n❌ Ошибок: %d", statusText, campaign.ID, campaign.Name, sent, blocked, unreachable, failed)
	w.h.sendBroadcastReport(ctx, w.h.cfg.TelegramAdminID, text, campaign.ID)
}

// Cancel cancels a running/scheduled campaign and prevents pending recipients
// from being claimed by the next worker pass.
func (w *BroadcastWorker) Cancel(ctx context.Context, id uint) error {
	_, err := w.h.db.CancelBroadcast(ctx, id, time.Now().UTC())
	if err != nil {
		return err
	}
	w.activeMu.Lock()
	cancel := w.active[id]
	w.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// RetryFailed makes failed recipients eligible again and reopens the campaign.
func (w *BroadcastWorker) RetryFailed(ctx context.Context, id uint) error {
	w.activeMu.Lock()
	cancel := w.active[id]
	w.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return w.h.db.ResetBroadcastFailedRecipients(ctx, id, time.Now().UTC())
}
