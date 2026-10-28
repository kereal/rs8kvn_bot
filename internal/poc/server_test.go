package poc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestSharedUpstreamIndependentExpiryAndRenewal(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	upstreamHits := 0
	s := New("http://secret-upstream.invalid")
	s.now = func() time.Time { return now }
	s.fetch = func(context.Context, string, http.Header) (*subserver.NodeResponse, error) {
		upstreamHits++
		return &subserver.NodeResponse{Body: []byte("vless://shared-config")}, nil
	}
	s.Seed("A", 10*time.Minute)
	s.Seed("B", 20*time.Minute)
	s.Seed("C", 30*time.Minute)
	call := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/sub/"+id, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, id := range []string{"A", "B", "C"} {
		require.Equal(t, 200, call(id).Code)
		require.Equal(t, "vless://shared-config", call(id).Body.String())
	}
	require.Equal(t, 1, upstreamHits)
	now = now.Add(11 * time.Minute)
	expired := call("A")
	require.Equal(t, http.StatusOK, expired.Code)
	require.Equal(t, "text/plain; charset=utf-8", expired.Header().Get("Content-Type"))
	require.Contains(t, expired.Body.String(), "Подписка закончилась")
	require.NotContains(t, expired.Body.String(), "secret-upstream.invalid")
	require.Equal(t, 200, call("B").Code)
	require.Equal(t, 200, call("C").Code)
	require.Equal(t, 1, upstreamHits)
	now = now.Add(10 * time.Minute)
	require.Equal(t, http.StatusOK, call("B").Code)
	require.Equal(t, 200, call("C").Code)
	now = now.Add(10 * time.Minute)
	require.Equal(t, http.StatusOK, call("C").Code)
	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	original := s.subs["A"].ID
	renewed, err := s.Renew("A", 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, original, renewed.ID)
	require.Equal(t, 200, call("A").Code)
}

func TestExpiredSubscriptionDoesNotFetchOrExposeUpstream(t *testing.T) {
	s := New("https://secret.example/token")
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	hits := 0
	s.fetch = func(context.Context, string, http.Header) (*subserver.NodeResponse, error) {
		hits++
		return &subserver.NodeResponse{Body: []byte("shared")}, nil
	}
	s.Seed("A", time.Minute)
	now = now.Add(2 * time.Minute)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/A", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	require.Contains(t, w.Body.String(), "Подписка закончилась")
	require.Zero(t, hits)
	require.NotContains(t, w.Body.String(), "secret.example")
	parsed, err := url.Parse(strings.TrimSpace(w.Body.String()))
	require.NoError(t, err)
	require.Equal(t, "vless", parsed.Scheme)
	require.Equal(t, "⏳ Подписка закончилась", parsed.Fragment)
}

func TestExpiredSubscriptionRenewalRestoresUpstreamFeed(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New("https://secret.example/token")
	s.now = func() time.Time { return now }
	hits := 0
	s.fetch = func(context.Context, string, http.Header) (*subserver.NodeResponse, error) {
		hits++
		return &subserver.NodeResponse{Body: []byte("vless://real-config")}, nil
	}
	s.Seed("A", time.Minute)

	now = now.Add(2 * time.Minute)
	expired := httptest.NewRecorder()
	s.ServeHTTP(expired, httptest.NewRequest(http.MethodGet, "/sub/A", nil))
	require.Equal(t, http.StatusOK, expired.Code)
	require.Contains(t, expired.Body.String(), "Подписка закончилась")
	require.Zero(t, hits)

	_, err := s.Renew("A", time.Minute)
	require.NoError(t, err)
	renewed := httptest.NewRecorder()
	s.ServeHTTP(renewed, httptest.NewRequest(http.MethodGet, "/sub/A", nil))
	require.Equal(t, http.StatusOK, renewed.Code)
	require.Equal(t, "vless://real-config", renewed.Body.String())
	require.Equal(t, 1, hits)
}

func TestPOCExpireEndpoint(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New("https://secret.example/token")
	s.now = func() time.Time { return now }
	s.Seed("A", time.Hour)

	expire := httptest.NewRecorder()
	s.ServeHTTP(expire, httptest.NewRequest(http.MethodGet, "/poc/A/expire", nil))
	require.Equal(t, http.StatusNoContent, expire.Code)

	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/poc/A", nil))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "Expired")
	require.Contains(t, page.Body.String(), "01.01.2026 03:00 MSK")

	feed := httptest.NewRecorder()
	s.ServeHTTP(feed, httptest.NewRequest(http.MethodGet, "/sub/A", nil))
	require.Equal(t, http.StatusOK, feed.Code)
	require.Contains(t, feed.Body.String(), "Подписка закончилась")
}

func TestUpstreamHeadersUseSharedTestHWID(t *testing.T) {
	s := New("https://relayhub.example/sub/token")
	for _, id := range []string{"A", "B", "C"} {
		s.Seed(id, time.Minute)
	}

	var gotURL string
	requests := make([]http.Header, 0, 1)
	s.fetch = func(_ context.Context, url string, headers http.Header) (*subserver.NodeResponse, error) {
		gotURL = url
		requests = append(requests, headers.Clone())
		return &subserver.NodeResponse{Body: []byte("vless://test")}, nil
	}

	for _, id := range []string{"A", "B", "C"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/"+id, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "vless://test", w.Body.String())
	}

	require.Len(t, requests, 1)
	gotHeaders := requests[0]
	require.Equal(t, "https://relayhub.example/sub/token", gotURL)
	require.Equal(t, "device-vpswindows10890", gotHeaders.Get("x-hwid"))
	require.Equal(t, "device-vpswindows10890", gotHeaders.Get("X-Device-ID"))
	require.Equal(t, "iOS", gotHeaders.Get("x-device-os"))
	require.Equal(t, "27.1", gotHeaders.Get("x-ver-os"))
	require.Equal(t, "iPhone 12", gotHeaders.Get("x-device-model"))
	require.Equal(t, "INCY", gotHeaders.Get("x-client"))
	require.Equal(t, "INCY/1.0.0/iOS", gotHeaders.Get("User-Agent"))
	require.Equal(t, 1, len(gotHeaders.Values("x-hwid")))
	require.Equal(t, "device-vpswindows10890", gotHeaders.Get("x-hwid"))
}

func TestPOCPageAndQRUseOnlyLocalURL(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New("https://secret-upstream.example/token")
	s.now = func() time.Time { return now }
	for _, id := range []string{"A", "B", "C"} {
		s.Seed(id, 10*time.Minute)
		page := httptest.NewRecorder()
		s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8890/poc/"+id, nil))
		require.Equal(t, http.StatusOK, page.Code)
		require.Contains(t, page.Body.String(), "Подписка "+id)
		require.Contains(t, page.Body.String(), "Активна")
		require.Contains(t, page.Body.String(), PublicBaseURL+"/sub/"+id)
		require.NotContains(t, page.Body.String(), "secret-upstream.example")

		qr := httptest.NewRecorder()
		s.ServeHTTP(qr, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8890/poc/"+id+"/qr.png", nil))
		require.Equal(t, http.StatusOK, qr.Code)
		require.Equal(t, "image/png", qr.Header().Get("Content-Type"))
		expectedQR, err := utils.GenerateQRCodePNG(PublicBaseURL + "/sub/" + id)
		require.NoError(t, err)
		require.Equal(t, expectedQR, qr.Body.Bytes())
		require.NotContains(t, qr.Body.String(), "secret-upstream.example")
	}

	now = now.Add(11 * time.Minute)
	expired := httptest.NewRecorder()
	s.ServeHTTP(expired, httptest.NewRequest(http.MethodGet, "/poc/A", nil))
	require.Equal(t, http.StatusOK, expired.Code)
	require.Contains(t, expired.Body.String(), "Expired")

	_, err := s.Renew("A", 10*time.Minute)
	require.NoError(t, err)
	renewed := httptest.NewRecorder()
	s.ServeHTTP(renewed, httptest.NewRequest(http.MethodGet, "/poc/A", nil))
	require.Contains(t, renewed.Body.String(), "Активна")
}

func TestPOCPageDisplaysExpiryInMoscowTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	s := New("https://secret-upstream.example/token")
	s.now = func() time.Time { return now }
	s.Seed("A", 50*time.Minute)

	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/poc/A", nil))

	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "01.01.2026 13:50 MSK")
	require.NotContains(t, page.Body.String(), "10:50 UTC")
}
