package poc

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/utils"
)

var ErrNotFound = errors.New("poc subscription not found or expired")

var displayLocation = func() *time.Location {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.UTC
	}
	return location
}()

// PublicBaseURL is the HTTPS address advertised in subscription pages and QR
// codes. The POC itself remains bound to loopback behind the reverse proxy.
const PublicBaseURL = "https://sub.gigachad-vape.indevs.in"

type Subscription struct {
	ID        string
	StartedAt time.Time
	ExpiresAt time.Time
}

type Server struct {
	upstream      string
	now           func() time.Time
	mu            sync.RWMutex
	subs          map[string]Subscription
	upstreamBody  map[string][]byte
	upstreamReady map[string]bool
	fetch         func(context.Context, string, http.Header) (*subserver.NodeResponse, error)
}

const (
	deviceOS        = "iOS"
	deviceOSVer     = "27.1"
	deviceModel     = "iPhone 12"
	clientName      = "INCY"
	clientUserAgent = "INCY/1.0.0/iOS"
	sharedTestHWID  = "device-vpswindows10890"
)

var pocHTTPClient = &http.Client{Timeout: 10 * time.Second}

var pocPage = template.Must(template.New("subscription").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Подписка {{.ID}}</title><style>
:root{color-scheme:light dark}body{margin:0;background:#f3f5f7;color:#17202a;font:16px system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:560px;margin:48px auto;padding:0 20px}.panel{background:#fff;border:1px solid #dfe4ea;border-radius:14px;padding:28px;box-shadow:0 8px 28px #17202a14}
h1{margin:0 0 24px;font-size:28px}.row{display:flex;justify-content:space-between;gap:16px;padding:12px 0;border-bottom:1px solid #edf0f2}.label{color:#65727e}.active{color:#16834b;font-weight:650}.expired{color:#b42318;font-weight:650}.url{display:flex;gap:8px;margin-top:8px}.url input{min-width:0;flex:1;padding:10px;border:1px solid #cbd3da;border-radius:8px;font:inherit;background:#fff;color:inherit}.url button{padding:10px 14px;border:0;border-radius:8px;background:#1667d9;color:#fff;font-weight:600;cursor:pointer}.qr{text-align:center;margin-top:24px}.qr img{width:220px;height:220px;image-rendering:auto}.hint{font-size:13px;color:#65727e;margin-top:8px}@media(prefers-color-scheme:dark){body{background:#111820;color:#edf2f7}.panel{background:#1b2530;border-color:#334252}.url input{background:#111820;border-color:#46576a}.label,.hint{color:#a8b4c0}}
</style></head><body><main><section class="panel"><h1>Подписка {{.ID}}</h1>
<div class="row"><span class="label">Статус</span><span class="{{.StatusClass}}">{{.Status}}</span></div>
<div class="row"><span class="label">Дата окончания</span><span>{{.ExpiresAt}}</span></div>
<div style="margin-top:22px"><div class="label">Локальная subscription URL</div><div class="url"><input id="subscription-url" readonly value="{{.LocalURL}}"><button type="button" onclick="navigator.clipboard.writeText(document.getElementById('subscription-url').value).then(()=>this.textContent='Скопировано')">Скопировать ссылку</button></div></div>
<div class="qr"><img src="{{.QRURL}}" alt="QR-код локальной подписки"><div class="hint">QR-код содержит только локальную ссылку</div></div>
</section></main></body></html>`))

type pageData struct {
	ID, Status, StatusClass, ExpiresAt, LocalURL, QRURL string
}

func New(upstream string) *Server {
	return &Server{
		upstream:      upstream,
		now:           time.Now,
		subs:          make(map[string]Subscription),
		upstreamBody:  make(map[string][]byte),
		upstreamReady: make(map[string]bool),
		fetch:         fetchUpstream,
	}
}

func requestHeaders() http.Header {
	headers := make(http.Header, 7)
	headers.Set("x-device-os", deviceOS)
	headers.Set("x-ver-os", deviceOSVer)
	headers.Set("x-device-model", deviceModel)
	headers.Set("x-client", clientName)
	headers.Set("User-Agent", clientUserAgent)
	headers.Set("x-hwid", sharedTestHWID)
	headers.Set("X-Device-ID", sharedTestHWID)
	return headers
}

func (s *Server) upstreamCacheKey() string {
	return s.upstream + "\x00shared-hwid"
}

func fetchUpstream(ctx context.Context, url string, headers http.Header) (*subserver.NodeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create POC upstream request: %w", err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := pocHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute POC upstream request: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("POC upstream returned no body")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POC upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read POC upstream response: %w", err)
	}
	if len(body) > config.MaxResponseSize {
		return nil, fmt.Errorf("POC upstream response exceeds %d bytes", config.MaxResponseSize)
	}

	return &subserver.NodeResponse{Body: body}, nil
}

func (s *Server) Seed(id string, duration time.Duration) Subscription {
	now := s.now().UTC()
	sub := Subscription{ID: id, StartedAt: now, ExpiresAt: now.Add(duration)}
	s.mu.Lock()
	s.subs[id] = sub
	s.mu.Unlock()
	return sub
}

func (s *Server) Renew(id string, duration time.Duration) (Subscription, error) {
	s.mu.Lock()
	sub, ok := s.subs[id]
	if !ok {
		s.mu.Unlock()
		return Subscription{}, ErrNotFound
	}
	sub.ExpiresAt = s.now().UTC().Add(duration)
	s.subs[id] = sub
	s.mu.Unlock()
	return sub, nil
}

// Expire is a local POC helper for manually exercising the expired feed.
// Production subscription APIs do not expose this operation.
func (s *Server) Expire(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[id]
	if !ok {
		return ErrNotFound
	}
	sub.ExpiresAt = s.now().UTC()
	s.subs[id] = sub
	return nil
}

func (s *Server) Get(id string) (Subscription, bool) {
	s.mu.RLock()
	sub, ok := s.subs[id]
	s.mu.RUnlock()
	return sub, ok
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/renew/") {
		id := strings.TrimPrefix(r.URL.Path, "/renew/")
		minutes, err := strconv.Atoi(r.URL.Query().Get("minutes"))
		if err != nil || minutes <= 0 {
			http.Error(w, "minutes must be positive", http.StatusBadRequest)
			return
		}
		if _, err = s.Renew(id, time.Duration(minutes)*time.Minute); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/poc/") {
		if strings.HasSuffix(r.URL.Path, "/qr.png") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/poc/"), "/qr.png")
			s.servePOCQR(w, r, id)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/expire") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/poc/"), "/expire")
			if err := s.Expire(id); err != nil {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.servePOCPage(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/sub/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	s.serveSubscription(w, r, id)
}

func (s *Server) serveSubscription(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	sub, ok := s.subs[id]
	if !ok {
		s.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	expired := !s.now().UTC().Before(sub.ExpiresAt)
	s.mu.RUnlock()
	if expired {
		s.serveExpiredSubscription(w)
		return
	}

	cacheKey := s.upstreamCacheKey()
	s.mu.Lock()
	body := append([]byte(nil), s.upstreamBody[cacheKey]...)
	ready := s.upstreamReady[cacheKey]
	s.mu.Unlock()
	if !ready {
		resp, err := s.fetch(r.Context(), s.upstream, requestHeaders())
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		body = append([]byte(nil), resp.Body...)
		s.mu.Lock()
		s.upstreamBody[cacheKey] = append([]byte(nil), body...)
		s.upstreamReady[cacheKey] = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	// Recheck expiry after the network call before publishing the response.
	current, exists := s.subs[id]
	if !exists {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	expired = !s.now().UTC().Before(current.ExpiresAt)
	s.mu.Unlock()
	if expired {
		s.serveExpiredSubscription(w)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) serveExpiredSubscription(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(expiredSubscriptionBody())
}

func (s *Server) localURL(r *http.Request, id string) string {
	return PublicBaseURL + "/sub/" + id
}

func (s *Server) servePOCPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/poc/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	sub, ok := s.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	active := s.now().UTC().Before(sub.ExpiresAt)
	status, class := "Expired", "expired"
	if active {
		status, class = "Активна", "active"
	}
	localURL := s.localURL(r, id)
	data := pageData{ID: id, Status: status, StatusClass: class, ExpiresAt: sub.ExpiresAt.In(displayLocation).Format("02.01.2006 15:04 MST"), LocalURL: localURL, QRURL: "/poc/" + id + "/qr.png"}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pocPage.Execute(w, data); err != nil {
		return
	}
}

func (s *Server) servePOCQR(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	png, err := utils.GenerateQRCodePNG(s.localURL(r, id))
	if err != nil {
		http.Error(w, "failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}
