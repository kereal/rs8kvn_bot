package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Контрактные тесты панели 3x-ui: форма конверта {success, msg, obj},
// вложенность карточки клиента и точные тексты ошибок из
// doc/architecture.md, раздел «Контракт API панели 3x-ui»
// (раздел 3, проверено на живой панели 2026-10-31).
// Тексты классифицируются в classify.go.

// newPanelFixture запускает панель-заглушку: GET /panel/api/inbounds/get/{id}
// отвечает минимальным inbound (нужен для группировки flow в update/add),
// остальные запросы — в handler тестируемого сценария.
func newPanelFixture(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/inbounds/get/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"tcp\"}"}}`)
	})
	mux.HandleFunc("/", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

func TestPanelContract_EnvelopeForm(t *testing.T) {
	t.Parallel()

	// Конверт: все клиентские эндпоинты отвечают HTTP 200 с {success,msg,obj};
	// распознаётся и success=false, и success=true с пустым obj.
	t.Run("success=false with msg", func(t *testing.T) {
		obj, err := parseEnvelope(opUpdate, []byte(`{"success":false,"msg":"Something went wrong (record not found)","obj":null}`))
		require.Nil(t, obj)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrClientNotFound)
	})

	t.Run("success=true with obj", func(t *testing.T) {
		obj, err := parseEnvelope(opUpdate, []byte(`{"success":true,"msg":"ok","obj":{"id":1}}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":1}`, string(obj))
	})

	t.Run("malformed json", func(t *testing.T) {
		_, err := parseEnvelope(opDel, []byte(`not json`))
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to parse del response")
	})
}

func TestPanelContract_Traffic_NullObjIsClientNotFound(t *testing.T) {
	t.Parallel()

	// Регрессия: панель отвечает success=true и obj=null на отсутствующий
	// email — это НЕ nil-ответ и НЕ нули трафика, а ErrClientNotFound.
	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/traffic/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	traffic, trafficErr := client.GetClientTraffic(context.Background(), "ghost@example.com")
	require.Error(t, trafficErr)
	assert.Nil(t, traffic, "obj=null must not be parsed into a zero traffic value")
	assert.ErrorIs(t, trafficErr, ErrClientNotFound)
}

func TestPanelContract_Traffic_ClientNotFoundMsg(t *testing.T) {
	t.Parallel()

	// Совместимость со старым поведением панели: success=false и
	// msg содержит "client not found".
	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"client not found","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	_, trafficErr := client.GetClientTraffic(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, trafficErr, ErrClientNotFound)
}

func TestPanelContract_Update_RecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/update/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.UpdateClient(context.Background(), ClientRequest{
		InboundIDs: []int{1}, CurrentEmail: "ghost@example.com", Email: "ghost@example.com",
		ClientID: "uuid", SubID: "sub", TrafficBytes: 1 << 30,
		ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: 30,
	})
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_Del_NotFoundInAnyInbound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/del/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (client \"ghost@example.com\" not found in any inbound or client record\n)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.DeleteClient(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_ResetTraffic_RecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_Get_ObtainRecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/get/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Obtain (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	record, getErr := client.GetClientByEmail(context.Background(), "ghost@example.com")
	require.Error(t, getErr)
	assert.Nil(t, record)
	assert.ErrorIs(t, getErr, ErrClientNotFound)
}

func TestPanelContract_Add_AlreadyInUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
	}{
		{"email in use", `{"success":false,"msg":"email already in use: dup@example.com","obj":null}`},
		{"subId in use", `{"success":false,"msg":"subId already in use: fb9de073cb\n","obj":null}`},
		{"duplicate", `{"success":false,"msg":"duplicate entry","obj":null}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.msg)
			})

			client, err := NewClient(server.URL, testAPIToken)
			require.NoError(t, err)
			defer client.Close()

			_, addErr := client.AddClientWithID(context.Background(), ClientRequest{
				InboundIDs: []int{1}, Email: "dup@example.com", ClientID: "uuid", SubID: "sub",
				TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: 30,
			})
			assert.ErrorIs(t, addErr, ErrClientAlreadyExists)
			assert.NotErrorIs(t, addErr, ErrClientNotFound)
		})
	}
}

func TestPanelContract_GetClientByEmail_Card(t *testing.T) {
	t.Parallel()

	// Форма ответа GET /panel/api/clients/get/{email} (контракт, раздел 2.1):
	// клиент лежит ВНУТРИ obj.client, inboundIds — рядом в obj.inboundIds.
	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/panel/api/clients/get/alpha@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"","obj":{`+
			`"client":{"id":"uuid-1","email":"alpha@example.com","enable":true,`+
			`"totalGB":128849018880,"expiryTime":1795899600000,"subId":"sub-1",`+
			`"reset":30,"resetDay":5,"resetMax":3,"resetWeekday":0,`+
			`"trafficReset":"monthly","trafficResetDay":1},`+
			`"externalLinks":[],"inboundIds":[2,4,5],`+
			`"tunnelAllowedIPs":null,"usedTraffic":null}}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	record, getErr := client.GetClientByEmail(context.Background(), "alpha@example.com")
	require.NoError(t, getErr)
	require.NotNil(t, record)

	assert.Equal(t, "uuid-1", record.Client.ID)
	assert.Equal(t, "alpha@example.com", record.Client.Email)
	assert.True(t, record.Client.Enable)

	// Полный профиль авто-renew, который /clients/traffic не отдаёт.
	assert.Equal(t, 30, record.Client.Reset)
	assert.Equal(t, 5, record.Client.ResetDay)
	assert.Equal(t, 3, record.Client.ResetMax)
	assert.Equal(t, "monthly", record.Client.TrafficReset)
	assert.Equal(t, 1, record.Client.TrafficResetDay)

	assert.Equal(t, []int{2, 4, 5}, record.InboundIDs)
}

// Регрессия B7: add обязан применять ту же нормализацию ResetDays, что и
// update (отрицательное значение → config.SubscriptionResetDay), чтобы add и
// update давали идентичный профиль. Дефект в проде не проявлялся — панель
// нормализует значение при записи; правка нужна для единообразия.
func TestPanelContract_Add_NormalizesNegativeResetDays(t *testing.T) {
	t.Parallel()

	var gotReset float64

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, rErr := io.ReadAll(r.Body)
		require.NoError(t, rErr)

		var payload struct {
			Client map[string]any `json:"client"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))

		reset, ok := payload.Client["reset"].(float64)
		require.True(t, ok, "reset must be a number in the add payload")
		gotReset = reset

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	_, addErr := client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1}, Email: "norm@example.com", ClientID: "uuid", SubID: "sub",
		TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: -1,
	})
	require.NoError(t, addErr)
	assert.Equal(t, float64(config.SubscriptionResetDay), gotReset,
		"add with ResetDays=-1 must send reset=config.SubscriptionResetDay, not -1")
}

// Распознавание текстов регистронезависимо; не распознанные контрактом
// тексты не превращаются в sentinel'ы.
func TestPanelContract_ClassifyFailure_CaseInsensitiveAndStrict(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, classifyFailure(opAdd, "EMAIL ALREADY IN USE: x@y.z"), ErrClientAlreadyExists)
	assert.ErrorIs(t, classifyFailure(opDel, `Client "x@y.z" NOT FOUND in any inbound or client record`), ErrClientNotFound)
	assert.ErrorIs(t, classifyFailure(opUpdate, "RECORD NOT FOUND"), ErrClientNotFound)

	// Чужие тексты — не sentinel'ы.
	assert.Nil(t, classifyFailure(opAdd, "inbound not found"))
	assert.Nil(t, classifyFailure(opDel, "something else"))
	// "not found" в другом контексте (traffic-ветка ищет только
	// "client not found") — не sentinel.
	assert.Nil(t, classifyFailure(opTraffic, "record not found"))
	assert.Nil(t, classifyFailure(opGet, "boom"))
}

// Панель отвечает не-200 только при инфраструктурных проблемах; по решению
// о ретраях это не ретраится (см. utils.IsRetryable) — фикстура-тест ниже
// закрепляет: не-200 не считается успехом и не классифицируется в sentinel.
func TestPanelContract_Non200IsNotRetriedAndNotClassified(t *testing.T) {
	t.Parallel()

	var calls int

	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `gateway error`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.DeleteClient(context.Background(), "ghost@example.com")
	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrNon200Response)
	assert.NotErrorIs(t, err, ErrClientNotFound)
	assert.Equal(t, 1, calls, "non-200 panel response must not be retried — current retry decision")
}
