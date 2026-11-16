package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createInviteTestSub(t *testing.T, svc *Service, telegramID int64, referredBy *int64, inviteCode *string) {
	t.Helper()

	sub := &Subscription{
		TelegramID:     telegramID,
		ClientID:       fmt.Sprintf("client-%d", telegramID),
		SubscriptionID: fmt.Sprintf("sub-%d", telegramID),
		Status:         string(SubscriptionStatusActive),
		ReferredBy:     referredBy,
		InviteCode:     inviteCode,
	}
	require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
}

func readInviteAuditEvents(t *testing.T, svc *Service, code string) []AuditLog {
	t.Helper()

	var events []AuditLog

	require.NoError(t, svc.GetDB().Model(&AuditLog{}).
		Where("entity_type = ? AND entity_id = ?", EntityInvite, code).
		Order("id ASC").Find(&events).Error)

	return events
}

func TestCreateInviteAdmin_ManualCodeWritesAudit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	invite := &Invite{Code: "manual01", ReferrerTGID: 1001}
	require.NoError(t, svc.CreateInviteAdmin(ctx, invite, InviteOriginManual))

	stored, err := svc.GetInviteByCode(ctx, "manual01")
	require.NoError(t, err)
	assert.Equal(t, int64(1001), stored.ReferrerTGID)

	events := readInviteAuditEvents(t, svc, "manual01")
	require.Len(t, events, 1)
	assert.Equal(t, AuditEventInviteCreated, events[0].Event)
	assert.Equal(t, string(ActorAdmin), string(events[0].ActorType))

	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[0].Details), &details))
	assert.Equal(t, "manual", details["origin"])
}

func TestCreateInviteAdmin_GeneratedOriginStillValidatesFormat(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Хендлер генерирует код 8 символов [0-9a-z] до вызова репозитория;
	// репозиторий принимает его и пишет origin="generated". Проверяем
	// фактически сохранённые значения, а не регекспом поле, которое
	// выставил сам тест.
	invite := &Invite{Code: "a7x9k2m1", ReferrerTGID: 1002}
	require.NoError(t, svc.CreateInviteAdmin(ctx, invite, InviteOriginGenerated))

	stored, err := svc.GetInviteByCode(ctx, "a7x9k2m1")
	require.NoError(t, err)
	assert.Equal(t, "a7x9k2m1", stored.Code, "the requested code must be persisted verbatim")
	assert.Equal(t, int64(1002), stored.ReferrerTGID)

	events := readInviteAuditEvents(t, svc, "a7x9k2m1")
	require.Len(t, events, 1)

	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[0].Details), &details))
	assert.Equal(t, "generated", details["origin"])
}

func TestCreateInviteAdmin_RejectsBadCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		code string
	}{
		{"uppercase", "A7X9K2M1"},
		{"too_short", "ab1"},
		{"too_long", "abcdefghijklmnopq"},
		{"hyphen", "abc-def1"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestService(t)
			// Формат кода валидируется одинаково для обоих origin'ов:
			// проверяем оба в одной таблице без её дублирования.
			err := svc.CreateInviteAdmin(context.Background(),
				&Invite{Code: tc.code, ReferrerTGID: 2001}, InviteOriginManual)
			require.ErrorIs(t, err, ErrInviteInvalid)

			err = svc.CreateInviteAdmin(context.Background(),
				&Invite{Code: tc.code, ReferrerTGID: 2001}, InviteOriginGenerated)
			require.ErrorIs(t, err, ErrInviteInvalid)
		})
	}
}

func TestCreateInviteAdmin_RejectsBadReferrer(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.CreateInviteAdmin(ctx, &Invite{Code: "zeroref01", ReferrerTGID: 0}, InviteOriginManual)
	require.ErrorIs(t, err, ErrInviteInvalid)

	err = svc.CreateInviteAdmin(ctx, &Invite{Code: "negref001", ReferrerTGID: -5}, InviteOriginManual)
	require.ErrorIs(t, err, ErrInviteInvalid)
}

func TestCreateInviteAdmin_DuplicateCode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "dupcode1", ReferrerTGID: 3001}, InviteOriginManual))

	err := svc.CreateInviteAdmin(ctx,
		&Invite{Code: "dupcode1", ReferrerTGID: 3002}, InviteOriginManual)
	require.ErrorIs(t, err, ErrInviteNameTaken)

	var taken *InviteNameTakenError
	require.ErrorAs(t, err, &taken)
	assert.Equal(t, "code", taken.Details["field"])
}

func TestCreateInviteAdmin_SecondCodeForSameReferrer(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "firstco1", ReferrerTGID: 4001}, InviteOriginManual))

	err := svc.CreateInviteAdmin(ctx,
		&Invite{Code: "secondc2", ReferrerTGID: 4001}, InviteOriginManual)
	require.ErrorIs(t, err, ErrInviteNameTaken)

	var taken *InviteNameTakenError
	require.ErrorAs(t, err, &taken)
	assert.Equal(t, "referrer_tg_id", taken.Details["field"])
}

func TestListInviteOverviews_CountsBothMetrics(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "withsubs1", ReferrerTGID: 5001}, InviteOriginManual))
	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "unusedc1", ReferrerTGID: 5002}, InviteOriginManual))

	referrer := int64(5001)
	code := "withsubs1"
	createInviteTestSub(t, svc, 6001, &referrer, &code) // привязанный + по коду
	// trial без привязки: только invite_code, referred_by пуст.
	createInviteTestSub(t, svc, -7001, nil, &code)

	rows, err := svc.ListInviteOverviews(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	byCode := make(map[string]InviteOverview, len(rows))
	for _, row := range rows {
		byCode[row.Code] = row
	}

	withSubs := byCode["withsubs1"]
	assert.Equal(t, int64(1), withSubs.ReferredCount, "один привязанный реферал")
	assert.Equal(t, int64(2), withSubs.SubscriptionsCount, "две подписки по коду, включая trial")

	unused := byCode["unusedc1"]
	assert.Equal(t, int64(0), unused.ReferredCount)
	assert.Equal(t, int64(0), unused.SubscriptionsCount)
}

func TestDeleteInvite_UnusedInviteIsRemoved(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "deletem1", ReferrerTGID: 8001}, InviteOriginManual))

	deleted, err := svc.DeleteInvite(ctx, "deletem1")
	require.NoError(t, err)
	assert.Equal(t, "deletem1", deleted.Code)
	assert.Equal(t, int64(8001), deleted.ReferrerTGID)

	_, err = svc.GetInviteByCode(ctx, "deletem1")
	require.ErrorIs(t, err, ErrInviteNotFound)

	events := readInviteAuditEvents(t, svc, "deletem1")
	require.Len(t, events, 2)
	assert.Equal(t, AuditEventInviteDeleted, events[1].Event)

	var snapshot map[string]any
	require.NotNil(t, events[1].OldState)
	require.NoError(t, json.Unmarshal([]byte(*events[1].OldState), &snapshot))
	assert.Equal(t, "deletem1", snapshot["code"])
	assert.Equal(t, float64(8001), snapshot["referrer_tg_id"])
}

func TestDeleteInvite_UsedInviteIsBlocked(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	require.NoError(t, svc.CreateInviteAdmin(ctx,
		&Invite{Code: "inuse001", ReferrerTGID: 9001}, InviteOriginManual))

	code := "inuse001"
	createInviteTestSub(t, svc, 9101, nil, &code)
	createInviteTestSub(t, svc, 9102, nil, &code)

	_, err := svc.DeleteInvite(ctx, "inuse001")
	require.ErrorIs(t, err, ErrInviteInUse)

	var inUse *InviteInUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, int64(2), inUse.Details["subscriptions"])

	// Строка осталась на месте.
	_, getErr := svc.GetInviteByCode(ctx, "inuse001")
	require.NoError(t, getErr)
}

func TestDeleteInvite_MissingCode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	_, err := svc.DeleteInvite(context.Background(), "nosuchc1")
	require.ErrorIs(t, err, ErrInviteNotFound)
}
