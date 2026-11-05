package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createInviteHTTPSub(t *testing.T, db *database.Service, telegramID int64, inviteCode string) {
	t.Helper()

	ctx := context.Background()
	plan, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)

	require.NoError(t, db.GetDB().Create(&database.Subscription{
		TelegramID:     telegramID,
		ClientID:       "client-" + inviteCode,
		SubscriptionID: "sub-" + inviteCode,
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         plan.ID,
		InviteCode:     &inviteCode,
	}).Error)
}

func TestAPI_InvitesListIncludesCounters(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	created := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"listitem1","referrer_tg_id":101}`)
	require.Equal(t, http.StatusCreated, created.Code)

	used := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"usedsub01","referrer_tg_id":102}`)
	require.Equal(t, http.StatusCreated, used.Code)
	createInviteHTTPSub(t, db, 201, "usedsub01")

	rec := do(t, srv, http.MethodGet, "/admin/api/invites", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []struct {
			Code               string    `json:"code"`
			ReferrerTGID       int64     `json:"referrer_tg_id"`
			ReferredCount      int64     `json:"referred_count"`
			SubscriptionsCount int64     `json:"subscriptions_count"`
			CreatedAt          time.Time `json:"created_at"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Total)
	require.Len(t, body.Items, 2)

	byCode := make(map[string]struct {
		ReferredCount      int64
		SubscriptionsCount int64
	}, len(body.Items))
	for _, item := range body.Items {
		assert.False(t, item.CreatedAt.IsZero(), "created_at must be present")
		byCode[item.Code] = struct {
			ReferredCount      int64
			SubscriptionsCount int64
		}{item.ReferredCount, item.SubscriptionsCount}
	}

	assert.Equal(t, int64(0), byCode["listitem1"].SubscriptionsCount)
	assert.Equal(t, int64(1), byCode["usedsub01"].SubscriptionsCount)
}

func TestAPI_InviteCreateManualAndGenerated(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	manual := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"manual03","referrer_tg_id":301}`)
	require.Equal(t, http.StatusCreated, manual.Code)

	var item struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(manual.Body.Bytes(), &item))
	assert.Equal(t, "manual03", item.Code)

	generated := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"referrer_tg_id":302}`)
	require.Equal(t, http.StatusCreated, generated.Code)

	var generatedItem struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(generated.Body.Bytes(), &generatedItem))
	assert.Regexp(t, `^[0-9a-z]{8}$`, generatedItem.Code)
}

func TestAPI_InviteCreateValidationAndUnknownField(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	badCode := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"BAD","referrer_tg_id":401}`)
	require.Equal(t, http.StatusBadRequest, badCode.Code)
	assert.Contains(t, badCode.Body.String(), `"validation"`)

	unknown := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"unknown1","referrer_tg_id":402,"extra":1}`)
	require.Equal(t, http.StatusBadRequest, unknown.Code)
	assert.Contains(t, unknown.Body.String(), `"bad_request"`)
}

func TestAPI_InviteCreateGuardsMapToConflict(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	require.Equal(t, http.StatusCreated, do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"guard001","referrer_tg_id":501}`).Code)

	duplicate := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"guard001","referrer_tg_id":502}`)
	require.Equal(t, http.StatusConflict, duplicate.Code)
	assert.Contains(t, duplicate.Body.String(), `"name_taken"`)
	assert.Contains(t, duplicate.Body.String(), `"field":"code"`)

	sameReferrer := do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"guard002","referrer_tg_id":501}`)
	require.Equal(t, http.StatusConflict, sameReferrer.Code)
	assert.Contains(t, sameReferrer.Body.String(), `"name_taken"`)
	assert.Contains(t, sameReferrer.Body.String(), `"field":"referrer_tg_id"`)
}

func TestAPI_InviteRequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/admin/api/invites", "", "").Code)
	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodPost, "/admin/api/invites", "",
		`{"code":"notoken1","referrer_tg_id":601}`).Code)
	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodDelete, "/admin/api/invites/notoken1", "", "").Code)
}

func TestAPI_InviteDeleteLifecycle(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	require.Equal(t, http.StatusCreated, do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"removable1","referrer_tg_id":701}`).Code)

	removed := do(t, srv, http.MethodDelete, "/admin/api/invites/removable1", testToken, "")
	require.Equal(t, http.StatusOK, removed.Code)
	assert.Contains(t, removed.Body.String(), `"code":"removable1"`)

	// Повторное удаление отсутствующей строки — 404 (ADR-0002 P13).
	missing := do(t, srv, http.MethodDelete, "/admin/api/invites/removable1", testToken, "")
	require.Equal(t, http.StatusNotFound, missing.Code)

	// Использованный инвайт удалить нельзя: 409 in_use с числом подписок.
	require.Equal(t, http.StatusCreated, do(t, srv, http.MethodPost, "/admin/api/invites", testToken,
		`{"code":"protected1","referrer_tg_id":702}`).Code)
	createInviteHTTPSub(t, db, 702, "protected1")

	inUse := do(t, srv, http.MethodDelete, "/admin/api/invites/protected1", testToken, "")
	require.Equal(t, http.StatusConflict, inUse.Code)
	assert.Contains(t, inUse.Body.String(), `"in_use"`)
	assert.Contains(t, inUse.Body.String(), `"subscriptions":1`)
}
