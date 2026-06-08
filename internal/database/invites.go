package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// GetInviteByReferrer returns the canonical (oldest) invite code for the given referrer.
// If the user has multiple historical codes (pre-005 duplicates), returns the one with the smallest created_at.
// Returns ErrInviteNotFound when no invite exists for this referrer.
func (s *Service) GetInviteByReferrer(ctx context.Context, referrerTGID int64) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).
		Where("referrer_tg_id = ?", referrerTGID).
		Order("created_at ASC, code ASC").
		First(&invite)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("failed to get invite by referrer: %w", result.Error)
	}
	return &invite, nil
}

// GetOrCreateInvite returns an existing invite for the referrer or creates a new one.
// It always returns the oldest (canonical) code for the user.
// After migration 005 the unique constraint guarantees at most one row per referrer_tg_id.
func (s *Service) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*Invite, error) {
	// First try to return existing canonical code (oldest)
	if existing, err := s.GetInviteByReferrer(ctx, referrerTGID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrInviteNotFound) {
		return nil, err
	}

	// No invite yet — create one with the proposed code
	now := time.Now()
	if err := s.db.WithContext(ctx).Exec(
		"INSERT INTO invites (code, referrer_tg_id, created_at) VALUES (?, ?, ?)",
		code, referrerTGID, now,
	).Error; err != nil {
		// Race: someone else just created it — read the canonical one
		if existing, err2 := s.GetInviteByReferrer(ctx, referrerTGID); err2 == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create invite after race: %w", err)
	}

	return &Invite{
		Code:         code,
		ReferrerTGID: referrerTGID,
		CreatedAt:    now,
	}, nil
}

// GetInviteByCode returns an invite by its code.
// Returns ErrInviteNotFound (such that errors.Is(err, ErrInviteNotFound) is true)
// when the code does not exist. Other errors are infrastructure failures (DB, etc).
func (s *Service) GetInviteByCode(ctx context.Context, code string) (*Invite, error) {
	var invite Invite
	result := s.db.WithContext(ctx).Where("code = ?", code).First(&invite)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("failed to get invite by code: %w", result.Error)
	}
	return &invite, nil
}

// GetReferralCount returns the number of referrals for a user.
func (s *Service) GetReferralCount(ctx context.Context, referrerTGID int64) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("referred_by = ?", referrerTGID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count referrals: %w", err)
	}
	return count, nil
}

// GetAllReferralCounts returns a map of referrer TGID to referral count.
func (s *Service) GetAllReferralCounts(ctx context.Context) (map[int64]int64, error) {
	type ReferralCount struct {
		ReferredBy int64
		Count      int64
	}
	var results []ReferralCount

	if err := s.db.WithContext(ctx).Model(&Subscription{}).
		Select("referred_by, COUNT(*) as count").
		Where("referred_by > 0").
		Group("referred_by").
		Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get referral counts: %w", err)
	}

	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.ReferredBy] = r.Count
	}
	return counts, nil
}
