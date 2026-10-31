package testutil

import (
	"context"
	"maps"
	"sync"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

var (
	_ interfaces.ReferralCountSource = (*ReferralCountSourceFake)(nil)
	_ interfaces.ReferralInviteStore = (*ReferralInviteStoreFake)(nil)
	_ interfaces.AuditLogWriter      = (*AuditLogWriterFake)(nil)
)

// ReferralCountSourceFake is a plain in-memory fake for the
// bot.ReferralCache seam: a map of telegram ID to referral count.
type ReferralCountSourceFake struct {
	mu     sync.RWMutex
	Counts map[int64]int64
}

// NewReferralCountSourceFake returns an empty ReferralCountSourceFake.
func NewReferralCountSourceFake() *ReferralCountSourceFake {
	return &ReferralCountSourceFake{Counts: make(map[int64]int64)}
}

func (f *ReferralCountSourceFake) GetAllReferralCounts(context.Context) (map[int64]int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	counts := make(map[int64]int64, len(f.Counts))
	maps.Copy(counts, f.Counts)

	return counts, nil
}

// ReferralInviteStoreFake is a plain in-memory fake for the
// bot.ReferralHandler seam: one invite per referrer plus a code index.
type ReferralInviteStoreFake struct {
	mu      sync.RWMutex
	Invites map[int64]*database.Invite // keyed by referrer TG ID
}

// NewReferralInviteStoreFake returns an empty ReferralInviteStoreFake.
func NewReferralInviteStoreFake() *ReferralInviteStoreFake {
	return &ReferralInviteStoreFake{Invites: make(map[int64]*database.Invite)}
}

func (f *ReferralInviteStoreFake) GetOrCreateInvite(_ context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if invite, ok := f.Invites[referrerTGID]; ok {
		return invite, nil
	}

	invite := &database.Invite{ReferrerTGID: referrerTGID, Code: code}
	f.Invites[referrerTGID] = invite

	return invite, nil
}

func (f *ReferralInviteStoreFake) GetInviteByReferrer(_ context.Context, referrerTGID int64) (*database.Invite, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	invite, ok := f.Invites[referrerTGID]
	if !ok {
		return nil, ErrRecordNotFound
	}

	return invite, nil
}

// AuditLogWriterFake is a plain in-memory fake for audit-log seams: it
// records every inserted entry for assertions.
type AuditLogWriterFake struct {
	mu        sync.Mutex
	AuditLogs []database.AuditLog
}

func (f *AuditLogWriterFake) InsertAuditLog(_ context.Context, e *database.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.AuditLogs = append(f.AuditLogs, *e)

	return nil
}
