package database

// This repository keeps audience snapshots and delivery outcomes in the
// broadcasts row, avoiding a separate recipient table while preserving recovery.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	broadcastRecipientLease = 2 * time.Minute
	broadcastRecipientBatch = 100
)

// BroadcastRecipient is an in-memory view of one entry in Broadcast.RecipientsState.
// ID is a stable position-derived identifier, not a database row ID.
type BroadcastRecipient struct {
	ID          uint                     `json:"id"`
	BroadcastID uint                     `json:"-"`
	TelegramID  int64                    `json:"telegram_id"`
	Status      BroadcastRecipientStatus `json:"status"`
	Attempts    int                      `json:"attempts"`
	LastError   string                   `json:"last_error,omitempty"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

type broadcastRecipientState struct {
	Snapshot   bool                 `json:"snapshot"`
	Recipients []BroadcastRecipient `json:"recipients"`
}

func emptyBroadcastRecipientState() broadcastRecipientState {
	return broadcastRecipientState{Recipients: []BroadcastRecipient{}}
}

func parseBroadcastRecipientState(b *Broadcast) (broadcastRecipientState, error) {
	state := emptyBroadcastRecipientState()
	if b.RecipientsState == "" || b.RecipientsState == "{}" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(b.RecipientsState), &state); err != nil {
		return state, fmt.Errorf("parse broadcast recipient state: %w", err)
	}
	if state.Recipients == nil {
		state.Recipients = []BroadcastRecipient{}
	}
	for i := range state.Recipients {
		state.Recipients[i].BroadcastID = b.ID
	}
	return state, nil
}

func setBroadcastRecipientState(b *Broadcast, state broadcastRecipientState) error {
	if state.Recipients == nil {
		state.Recipients = []BroadcastRecipient{}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal broadcast recipient state: %w", err)
	}
	b.RecipientsState = string(data)
	return nil
}

func updateBroadcastRecipientState(ctx context.Context, s *Service, broadcastID uint, mutate func(*Broadcast, *broadcastRecipientState) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var broadcast Broadcast
		if err := tx.First(&broadcast, broadcastID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBroadcastNotFound
			}
			return fmt.Errorf("load broadcast state: %w", err)
		}
		state, err := parseBroadcastRecipientState(&broadcast)
		if err != nil {
			return err
		}
		if err := mutate(&broadcast, &state); err != nil {
			return err
		}
		if err := setBroadcastRecipientState(&broadcast, state); err != nil {
			return err
		}
		result := tx.Model(&Broadcast{}).Where("id = ?", broadcastID).Update("recipients_state", broadcast.RecipientsState)
		if result.Error != nil {
			return fmt.Errorf("save broadcast recipient state: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrBroadcastNotFound
		}
		return nil
	})
}

// SnapshotBroadcastRecipients materializes an immutable audience in broadcasts.
// Repeated calls return the original snapshot and never add newly matching users.
func (s *Service) SnapshotBroadcastRecipients(ctx context.Context, broadcastID uint, filter BroadcastFilter) (int64, error) {
	var ids []int64
	query := s.broadcastAudienceQuery(ctx, filter).Distinct("telegram_id").Order("telegram_id ASC").Pluck("telegram_id", &ids)
	if query.Error != nil {
		return 0, fmt.Errorf("select broadcast audience: %w", query.Error)
	}
	returnValue := int64(0)
	err := updateBroadcastRecipientState(ctx, s, broadcastID, func(_ *Broadcast, state *broadcastRecipientState) error {
		if state.Snapshot {
			returnValue = int64(len(state.Recipients))
			return nil
		}
		state.Snapshot = true
		state.Recipients = make([]BroadcastRecipient, 0, len(ids))
		now := time.Now().UTC()
		for i, telegramID := range ids {
			state.Recipients = append(state.Recipients, BroadcastRecipient{
				ID: uint(i + 1), BroadcastID: broadcastID, TelegramID: telegramID,
				Status: BroadcastRecipientPending, UpdatedAt: now,
			})
		}
		returnValue = int64(len(state.Recipients))
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("snapshot broadcast recipients: %w", err)
	}
	return returnValue, nil
}

// ClaimBroadcast atomically changes a scheduled campaign to running.
func (s *Service) ClaimBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&Broadcast{}).
		Where("id = ? AND status = ?", id, BroadcastStatusScheduled).
		Updates(map[string]any{"status": BroadcastStatusRunning, "started_at": now, "planned_at": now})
	if result.Error != nil {
		return false, fmt.Errorf("claim broadcast: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := s.db.WithContext(ctx).Model(&Broadcast{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return false, fmt.Errorf("check broadcast claim: %w", err)
		}
		if count == 0 {
			return false, ErrBroadcastNotFound
		}
	}
	return result.RowsAffected == 1, nil
}

// RecoverStaleBroadcastRecipients releases leases left by a crashed worker.
func (s *Service) RecoverStaleBroadcastRecipients(ctx context.Context, broadcastID uint, before time.Time) error {
	return updateBroadcastRecipientState(ctx, s, broadcastID, func(_ *Broadcast, state *broadcastRecipientState) error {
		for i := range state.Recipients {
			if state.Recipients[i].Status == BroadcastRecipientSending && state.Recipients[i].UpdatedAt.Before(before) {
				state.Recipients[i].Status = BroadcastRecipientPending
				state.Recipients[i].UpdatedAt = time.Now().UTC()
			}
		}
		return nil
	})
}

// ClaimBroadcastRecipients claims a batch in the JSON state. The database
// transaction serializes claims because SQLite uses one writer at a time.
func (s *Service) ClaimBroadcastRecipients(ctx context.Context, broadcastID uint, now time.Time, limit int) ([]BroadcastRecipient, error) {
	if limit <= 0 {
		limit = broadcastRecipientBatch
	}
	var claimed []BroadcastRecipient
	err := updateBroadcastRecipientState(ctx, s, broadcastID, func(b *Broadcast, state *broadcastRecipientState) error {
		if b.Status != string(BroadcastStatusRunning) {
			return nil
		}
		for i := range state.Recipients {
			if len(claimed) >= limit {
				break
			}
			if state.Recipients[i].Status != BroadcastRecipientPending {
				continue
			}
			state.Recipients[i].Status = BroadcastRecipientSending
			state.Recipients[i].Attempts++
			state.Recipients[i].UpdatedAt = now
			claimed = append(claimed, state.Recipients[i])
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim broadcast recipient batch: %w", err)
	}
	return claimed, nil
}

// FinishBroadcastRecipient records a terminal outcome for one recipient.
func (s *Service) FinishBroadcastRecipient(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, status BroadcastRecipientStatus, lastError string, now time.Time) error {
	if status != BroadcastRecipientSent && status != BroadcastRecipientBlocked && status != BroadcastRecipientUnreachable && status != BroadcastRecipientFailed {
		return fmt.Errorf("invalid broadcast recipient status: %s", status)
	}
	return updateBroadcastRecipientState(ctx, s, broadcastID, func(_ *Broadcast, state *broadcastRecipientState) error {
		for i := range state.Recipients {
			recipient := &state.Recipients[i]
			if recipient.ID != id {
				continue
			}
			if recipient.Status != BroadcastRecipientSending || recipient.Attempts != expectedAttempts {
				return ErrBroadcastRecipientStale
			}
			recipient.Status = status
			recipient.LastError = lastError
			recipient.UpdatedAt = now
			return nil
		}
		// The broadcast exists but this recipient is not in its snapshot:
		// report the recipient as missing, not the whole campaign.
		return ErrBroadcastRecipientNotFound
	})
}

// CancelBroadcast marks a campaign canceled and releases all sending leases
// in the same transaction. The worker context is canceled separately by the
// bot layer, so no new recipient can be claimed after this transition.
func (s *Service) CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error) {
	var canceled bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var broadcast Broadcast
		if err := tx.First(&broadcast, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBroadcastNotFound
			}
			return fmt.Errorf("load broadcast for cancellation: %w", err)
		}
		if broadcast.Status != string(BroadcastStatusScheduled) && broadcast.Status != string(BroadcastStatusRunning) {
			return nil
		}
		state, err := parseBroadcastRecipientState(&broadcast)
		if err != nil {
			return err
		}
		for i := range state.Recipients {
			if state.Recipients[i].Status == BroadcastRecipientSending {
				state.Recipients[i].Status = BroadcastRecipientPending
				state.Recipients[i].UpdatedAt = now
			}
		}
		if err := setBroadcastRecipientState(&broadcast, state); err != nil {
			return err
		}
		result := tx.Model(&Broadcast{}).Where("id = ? AND status IN ?", id, []BroadcastStatus{BroadcastStatusScheduled, BroadcastStatusRunning}).
			Updates(map[string]any{"status": BroadcastStatusCanceled, "finished_at": now, "recipients_state": broadcast.RecipientsState})
		if result.Error != nil {
			return fmt.Errorf("cancel broadcast: %w", result.Error)
		}
		canceled = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return canceled, nil
}

// ResetBroadcastFailedRecipients makes failed recipients eligible for manual retry
// and reopens a terminal campaign in the same transaction.
func (s *Service) ResetBroadcastFailedRecipients(ctx context.Context, id uint, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var broadcast Broadcast
		if err := tx.First(&broadcast, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBroadcastNotFound
			}
			return fmt.Errorf("load broadcast for retry: %w", err)
		}
		state, err := parseBroadcastRecipientState(&broadcast)
		if err != nil {
			return err
		}
		for i := range state.Recipients {
			if state.Recipients[i].Status == BroadcastRecipientFailed || state.Recipients[i].Status == BroadcastRecipientSending {
				state.Recipients[i].Status = BroadcastRecipientPending
				state.Recipients[i].UpdatedAt = now
				state.Recipients[i].LastError = ""
			}
		}
		if err := setBroadcastRecipientState(&broadcast, state); err != nil {
			return err
		}
		updates := map[string]any{
			"recipients_state": broadcast.RecipientsState,
			"last_error":       "",
			"retry_at":         nil,
			"retry_count":      0,
		}
		if broadcast.Status == string(BroadcastStatusCompleted) || broadcast.Status == string(BroadcastStatusFailed) || broadcast.Status == string(BroadcastStatusCanceled) {
			updates["status"] = BroadcastStatusRunning
			updates["finished_at"] = nil
		}
		result := tx.Model(&Broadcast{}).Where("id = ?", id).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("save broadcast retry state: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrBroadcastNotFound
		}
		return nil
	})
}

// GetRunnableBroadcasts returns campaigns that can be resumed after restart.
func (s *Service) GetRunnableBroadcasts(ctx context.Context, now time.Time) ([]Broadcast, error) {
	var broadcasts []Broadcast
	result := s.db.WithContext(ctx).
		Where("status IN ? AND (retry_at IS NULL OR retry_at <= ?)", []BroadcastStatus{BroadcastStatusScheduled, BroadcastStatusRunning}, now).
		Order("id ASC").Find(&broadcasts)
	if result.Error != nil {
		return nil, fmt.Errorf("get runnable broadcasts: %w", result.Error)
	}
	return broadcasts, nil
}

// GetBroadcastRecipientsStats returns counts from the JSON recipient state.
func (s *Service) GetBroadcastRecipientsStats(ctx context.Context, broadcastID uint) (total, sent, blocked, unreachable, failed int64, report BroadcastDeliveryReport, err error) {
	b, err := s.GetBroadcast(ctx, broadcastID)
	if err != nil {
		return 0, 0, 0, 0, 0, report, err
	}
	state, err := parseBroadcastRecipientState(b)
	if err != nil {
		return 0, 0, 0, 0, 0, report, err
	}
	for _, recipient := range state.Recipients {
		total++
		switch recipient.Status {
		case BroadcastRecipientSent:
			sent++
			report.Delivered = append(report.Delivered, recipient.TelegramID)
		case BroadcastRecipientBlocked:
			blocked++
			report.Blocked = append(report.Blocked, recipient.TelegramID)
		case BroadcastRecipientUnreachable:
			unreachable++
			report.Unreachable = append(report.Unreachable, recipient.TelegramID)
		case BroadcastRecipientFailed:
			failed++
			report.Errors = append(report.Errors, BroadcastSendError{TelegramID: recipient.TelegramID, Error: truncateDatabaseError(recipient.LastError)})
		case BroadcastRecipientPending, BroadcastRecipientSending:
			report.NotProcessed = append(report.NotProcessed, recipient.TelegramID)
		}
	}
	if report.Delivered == nil {
		report.Delivered = []int64{}
	}
	if report.Blocked == nil {
		report.Blocked = []int64{}
	}
	if report.Unreachable == nil {
		report.Unreachable = []int64{}
	}
	if report.Errors == nil {
		report.Errors = []BroadcastSendError{}
	}
	if report.NotProcessed == nil {
		report.NotProcessed = []int64{}
	}
	return total, sent, blocked, unreachable, failed, report, nil
}

func truncateDatabaseError(value string) string {
	const max = 500
	if len([]rune(value)) <= max {
		return value
	}
	return string([]rune(value)[:max]) + "…"
}

// broadcastAudienceQuery is shared by preview/count and snapshot. Trials are
// never eligible because they use a trial plan or a non-positive Telegram ID.
func (s *Service) broadcastAudienceQuery(ctx context.Context, filter BroadcastFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&Subscription{}).Where("telegram_id > 0")
	q = applyBroadcastFilter(q, filter)
	return q
}

func applyBroadcastFilter(q *gorm.DB, filter BroadcastFilter) *gorm.DB {
	if filter.SubscriptionStatus == "expired" {
		return q.Where("1 = 0")
	}
	if filter.SubscriptionStatus != "" && filter.SubscriptionStatus != "all" {
		q = q.Where("status = ?", filter.SubscriptionStatus)
	} else if filter.SubscriptionStatus == "" {
		q = q.Where("status = ?", string(SubscriptionStatusActive))
	}
	// NOT IN (not !=) so a missing standard trial plan degrades to a no-op
	// instead of UNKNOWN: a scalar subquery without rows turns `!=` into
	// `plan_id != NULL`, which filters out the whole audience.
	// NOT IN (not !=) so a missing standard trial plan degrades to a no-op
	// instead of UNKNOWN: a scalar subquery without rows turns `!=` into
	// `plan_id != NULL`, which filters out the whole audience.
	q = q.Where("plan_id NOT IN (SELECT id FROM plans WHERE name = ?)", TrialPlanName)
	switch filter.PlanType {
	case "paid":
		q = q.Where("(product_id IS NOT NULL OR price_paid_cents > 0)")
	case "free":
		q = q.Where("plan_id = (SELECT id FROM plans WHERE name = ?)", FreePlanName).Where("product_id IS NULL AND price_paid_cents <= 0")
	}
	if filter.RegisteredAfter != nil {
		q = q.Where("created_at >= ?", *filter.RegisteredAfter)
	}
	if filter.RegisteredBefore != nil {
		q = q.Where("created_at <= ?", *filter.RegisteredBefore)
	}
	if filter.InactiveDays != nil {
		if *filter.InactiveDays == 0 {
			q = q.Where("last_request IS NULL")
		} else if *filter.InactiveDays > 0 {
			q = q.Where(fmt.Sprintf("last_request < datetime('now', '-%d days')", *filter.InactiveDays))
		}
	}
	if filter.EverPaid != nil {
		if *filter.EverPaid {
			q = q.Where("EXISTS (SELECT 1 FROM orders WHERE orders.subscription_id = subscriptions.id AND orders.status = ?)", OrderStatusPaid)
		} else {
			q = q.Where("NOT EXISTS (SELECT 1 FROM orders WHERE orders.subscription_id = subscriptions.id AND orders.status = ?)", OrderStatusPaid)
		}
	}
	return q
}
