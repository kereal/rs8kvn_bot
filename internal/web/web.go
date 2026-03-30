package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

type ComponentHealth struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

type Server struct {
	addr        string
	db          interfaces.DatabaseService
	xuiClient   interfaces.XUIClient
	cfg         *config.Config
	botUsername string
	server      *http.Server
	mu          sync.RWMutex
	ready       bool
	checkers    map[string]func(context.Context) ComponentHealth
}

func NewServer(addr string, db interfaces.DatabaseService, xuiClient interfaces.XUIClient, cfg *config.Config, botUsername string) *Server {
	return &Server{
		addr:        addr,
		db:          db,
		xuiClient:   xuiClient,
		cfg:         cfg,
		botUsername: botUsername,
		checkers:    make(map[string]func(context.Context) ComponentHealth),
	}
}

func (s *Server) RegisterChecker(name string, checker func(context.Context) ComponentHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers[name] = checker
}

func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/i/", s.handleInvite)

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	logger.Info("Web server started", zap.String("addr", s.addr))
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := s.checkHealth(ctx)
	s.writeJSON(w, health)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := s.checkHealth(ctx)

	if health.Status == "ok" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("NOT READY"))
	}
}

type HealthResponse struct {
	Status     string                 `json:"status"`
	Components map[string]HealthCheck `json:"components"`
	Timestamp  time.Time              `json:"timestamp"`
}

type HealthCheck struct {
	Status  string        `json:"status"`
	Latency time.Duration `json:"latency,omitempty"`
	Error   string        `json:"error,omitempty"`
}

func (s *Server) checkHealth(ctx context.Context) HealthResponse {
	checkers := map[string]func(context.Context) HealthCheck{
		"database": s.checkDatabase,
		"xui":      s.checkXUI,
	}

	response := HealthResponse{
		Status:     "ok",
		Components: make(map[string]HealthCheck),
		Timestamp:  time.Now(),
	}

	for name, checker := range checkers {
		component := checker(ctx)
		response.Components[name] = component

		if component.Status == "down" {
			response.Status = "down"
		} else if component.Status == "degraded" && response.Status == "ok" {
			response.Status = "degraded"
		}
	}

	return response
}

func (s *Server) checkDatabase(ctx context.Context) HealthCheck {
	start := time.Now()
	err := s.db.Ping(ctx)
	latency := time.Since(start)

	if err != nil {
		return HealthCheck{
			Status:  "down",
			Error:   err.Error(),
			Latency: latency,
		}
	}

	return HealthCheck{
		Status:  "ok",
		Latency: latency,
	}
}

func (s *Server) checkXUI(ctx context.Context) HealthCheck {
	start := time.Now()
	err := s.xuiClient.Ping(ctx)
	latency := time.Since(start)

	if err != nil {
		return HealthCheck{
			Status:  "down",
			Error:   err.Error(),
			Latency: latency,
		}
	}

	return HealthCheck{
		Status:  "ok",
		Latency: latency,
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, resp HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("Failed to encode JSON response", zap.Error(err))
	}
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	path := r.URL.Path
	if !strings.HasPrefix(path, "/i/") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(s.renderErrorPage("Страница не найдена")))
		return
	}

	code := path[3:]
	if code == "" || strings.Contains(code, "/") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(s.renderErrorPage("Приглашение не найдено")))
		return
	}

	invite, err := s.db.GetInviteByCode(ctx, code)
	if err != nil {
		logger.Warn("Invite not found", zap.String("code", code), zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(s.renderErrorPage("Приглашение не найдено")))
		return
	}

	ip := getClientIP(r)

	count, err := s.db.CountTrialRequestsByIPLastHour(ctx, ip)
	if err != nil {
		logger.Error("Failed to check rate limit", zap.Error(err), zap.String("ip", ip))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка сервера. Попробуйте позже.")))
		return
	}
	if count >= s.cfg.TrialRateLimit {
		logger.Warn("Rate limit exceeded", zap.String("ip", ip), zap.Int("count", count))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(s.renderErrorPage("Слишком много запросов. Попробуйте позже.")))
		return
	}

	if err := s.db.CreateTrialRequest(ctx, ip); err != nil {
		logger.Error("Failed to create trial request", zap.Error(err))
	}

	subID := utils.GenerateSubID()

	trafficBytes := int64(s.cfg.TrialDurationHours) * 1024 * 1024 * 1024 / (24 * 365 / (30 * 24))
	if trafficBytes < 1024*1024*1024 {
		trafficBytes = 1024 * 1024 * 1024
	}
	expiryTime := time.Now().Add(time.Duration(s.cfg.TrialDurationHours) * time.Hour)

	if err := s.xuiClient.Login(ctx); err != nil {
		logger.Error("Failed to login to xui", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка сервера. Попробуйте позже.")))
		return
	}

	clientID := utils.GenerateUUID()
	_, err = s.xuiClient.AddClientWithID(ctx, s.cfg.XUIInboundID, "trial_"+subID, clientID, subID, trafficBytes, expiryTime, 0)
	if err != nil {
		logger.Error("Failed to create trial client in xui", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка создания подписки. Попробуйте позже.")))
		return
	}

	subURL := s.xuiClient.GetSubscriptionLink(s.xuiClient.GetExternalURL(s.cfg.XUIHost), subID, s.cfg.XUISubPath)

	_, err = s.db.CreateTrialSubscription(ctx, code, subID, clientID, s.cfg.XUIInboundID, trafficBytes, expiryTime, subURL)
	if err != nil {
		logger.Error("Failed to create trial subscription in DB", zap.Error(err))
		s.xuiClient.DeleteClient(ctx, s.cfg.XUIInboundID, clientID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(s.renderErrorPage("Ошибка сервера. Попробуйте позже.")))
		return
	}

	logger.Info("Trial subscription created",
		zap.String("code", code),
		zap.String("subscription_id", subID),
		zap.String("ip", ip),
		zap.Int64("referrer_tg_id", invite.ReferrerTGID))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	telegramLink := "https://t.me/" + s.botUsername + "?start=trial_" + subID
	w.Write([]byte(s.renderTrialPage(subID, subURL, telegramLink, s.cfg.TrialDurationHours)))
}

func (s *Server) renderTrialPage(subID, subURL, telegramLink string, trialHours int) string {
	happLink := "happ://add/" + subURL
	safeSubURL := html.EscapeString(subURL)
	safeTelegramLink := html.EscapeString(telegramLink)
	safeHappLink := html.EscapeString(happLink)

	html := `<!DOCTYPE html>
<html lang="ru">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>RS8 KVN</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
        .container { background: white; border-radius: 20px; box-shadow: 0 20px 60px rgba(0,0,0,0.3); padding: 32px; max-width: 400px; width: 100%; text-align: center; }
        h2 { color: #333; margin: 24px 0 12px; font-size: 18px; }

        p { color: #666; line-height: 1.6; margin-bottom: 20px; }
        .btn { display: block; width: 100%; padding: 14px 20px; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; border: none; border-radius: 12px; font-size: 16px; font-weight: 600; cursor: pointer; text-decoration: none; margin-bottom: 10px; transition: transform 0.2s, box-shadow 0.2s; }
        .btn:hover { transform: translateY(-2px); box-shadow: 0 10px 30px rgba(102, 126, 234, 0.4); }
        .btn-secondary { background: #f0f0f0; color: #333; }
        .btn-secondary:hover { background: #e0e0e0; box-shadow: none; }
        .btn-row { display: flex; gap: 10px; }
        .btn-row .btn { flex: 1; }
        .info { background: #f8f9fa; border-radius: 12px; padding: 16px; margin-bottom: 20px; }
        .info-row { display: flex; justify-content: space-between; padding: 8px 0; }
        .info-label { color: #666; }
        .info-value { color: #333; font-weight: 500; }
        .note { font-size: 14px; color: #9b59b6; margin-top: 20px; font-weight: 500; }
        .divider { height: 1px; background: #eee; margin: 20px 0; }
        .copy-link { color: #666; text-decoration: underline; font-size: 14px; cursor: pointer; display: inline-block; margin-top: 8px; text-decoration: none; }
        .logo { margin-bottom: 24px; }
    </style>
</head>
<body>
    <div class="container">
        <img class="logo" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAPAAAACGCAMAAADU8Pd8AAAAclBMVEX+/v3////6+/orPEru8fIbLT3d4ePEys2ss7iVnqSBjJQ+TltpdoBVY3ACEiYMIzgKIDUJHzQGHDEIGCpuPiv3hDH2eyP2cS73aSvzYyblazZKLSctHiTJYzWtWTOOSTDdeTllHy2WJzHDLTPlJyvpRizVoAj6AAAY4ElEQVR42tSaC2OiOhOGReQmkJBEbU8BL3X3///FL+9MLuKt0j3f7tmXatEq5GEmk5mhi9+hpdUiIa3yoqyqddO2bU2yO826qsoiXyWkBT78N8vD5kW1bus0k9pAwj60ld0Rwu7KLK2bdVmsPPVfxXgFuyyqps4smLCMqpP31CktrHRWNxVT/12WTkDLli2btANqR1wP5bg1DJ7Va0CzoZPkb/Bihl0V6zoTgli7F0XUgG6rYkmG/itQk2VeWlgtjALBbBG0Stdg/k9O6CWUONSiXLdpZiwsWNVsWqVUh6neGWHS5k8zL+024YRNA2oFVI3oo6QVjT5whFfyevJGVnyEkflPWhhrZ+faf4w6YHrSlUVtyKocngJEYIywSluZqbSOlyF+NDDrusoJGfoD3BHTei+vrp0BqboXneCfCjiKMYdh6PuN1YGEvb4fBuImR1ZQF8S+nbVs5j/i06ty3bS1xcykhk0xUmdUP9Yofqk1SPvT5rDf/xx//vPznwt9WI17y07cWitv5HgMqYSpq9WfQV4u4cC1FGBVkhR4b8xDrED9PP+8o8D+/v7+RtxMDUyazV4wc7rOEcD+H0x4fOHSKwTj2hmZ4Z37MjNgFcMG1giJ7Y4s9dvHeNgMxkNDfETM5q4tCPl3a1oNcDHQ1jBxFEw79J9nhv0xpcXTyNs4fny8kywvQb8RdD+IS2aIPbtcROTfmohNQ3XRpATsYg5May3746fd8BSIabKeesQpp95qYye3BX97Y24wv437DTErKE4RY5GXQP4XDZ0A5UVkny8boUPSEGmtgMy4Z5AO5K0QOGiFoicKaoT9Bmxi3o172FnFYEiebQSszANg/arpVq4ufcW6ucuXpaSAHGmhQHs+b04cgnXMPAAipY/1gstFUAdoMB96upZhMncOeTXJBH4Je7VuytzXpSwgBi1iYsVlnw7Lr9YXtGxhC/t5IlYl2emDAIwyuCoRB+q0c9Sq3+xHYn57f9sdybXjEsDIJk3Tum7x5SJfBuxvErciq5uyWC2Tu1qFdFkIrJoxJkdaCKb9hGW1glHvLtOWD9AlBp3jqJYFrj70BzBDu93Rmtngm7Cxn8sQeYbJ0rapgP1NaPvFBo0IleEKlmVZFLlVUbj2TM2owIgLh76hhW1hWr4krCvc4BZcD+bkN1WbwYmVIeadQ94zcvQQGaXoADJt10Xuy+iZ2EmlNFfjTAafxWFZbDEVFObtFPezHwyuBQE/Fvso14NNkSysB7kgqM2w2R8d8m6/MYZd+o6kGy16B8vZzIk1cpEJKX0fwg1aQg5V6wvYQBuFeau7l8pDHJuglaWs1zl5ZtFkQnRSm/5w3DnmEch3vh8SPFDjspUrMM9067wVWt49NNTxkuJgI23EZU+eJa4TZFNYYNi57YAshs1oke222xLyNBTwnh8UH0JkVEYDmR4vTuQyE528ywtSZgXsrc4nIf1gZjMroVpCXiRFI4WW2gD5kZVlGBse3r19rTErkUpWjRLkwW6iDWowA3Q6WdRr1vOPs5WbwCdDp58tSpo7oZo8wQiSfJ3BU4whZPxs972BBvzEehpS2DiwuVpjBnKSwMhFq0Ro1Ay947wkBaV982Q1sE7wcSL+hoXZX6USWbXkoruS9j3ZwcpvRGyRrUbWHpkq15WGqWPtokW2Xs0qKIGM4GE6SdDagAf6PAHSUxpIQ0Y7bz9tBv1NAzOyFjUt9lLDQ/mYRIwHa7vbOh2P4x7lNEPHlEzjus316ySvagVm2IupjBEk/CZU5fLiy0kOx5ovBoY6Oo0GbMw3d3j4Zy/etdgoN8AcQxhdt1nECZAXxbruhFGkyaiwowkVP9g89Pd4fUUNSSsFWFtRgAiM7/QcNEVm6A0yFIzK5d2iq5LZZWDCdp4IeCR9oXgpoG/y+iKEYWHYN6qdSMfjETPXaxztG9a3PTmYkXtrn5NhcjSvx654D6FqU+UUkLx7mwHioOmaUt9GlhNYrprAytN008czYTYp7gmirDxaVjK2/T0ycufSJdEuZ3RiFznBYjopLxqS4IhsdWZxuI4dyO5LZjVBlb6zGQtj7n64iATBrzqJzQuBBej2S5Ya0LDz8TAYwsUgHhMvr5rrq4sCMKYzOMG9nMN3Nc77T46YXxtZe902AYD64VFdpQK+KBpJlBACuTeYCXkzmI5nCIjBNkH1SibFrvaN9XA3wMP+uC/Xt7IPdOKEfj5hZdanfd/7Lg9QPzwpoQ5AxQDuu3+W4ksQdXn7wVKDmZHHXijpFiixRurmlUCRtKwalKWBFbrpZkBn1oWB4dQHm3bsqSdJq4R6WiVlAPVpBJzXD53bI8/9A+0STGBLCO38cox6g+LXwZiwyJVMvAz3ELj7yHcR4r2hqEgLUEo7hpNPrXwD60wzTbmeOwzVP0XGiq3x+aAQ9/z5nzD7wIlJP7pIjSzEXrMPfjHCrcmXdJaTV6/WdV2n/J8IodSNZg0KtGjYcHsqiv54OqM1+Wlc/OYJebBW2wwPkGXnC1B+QRE1qntJ/j46ysitS8Ngbdb22DsbS9EkAE7yKhVCTU4k49F8aIAVYVfXsJHT8MGh0l4Q69ODVlBcSjfWSQfzwFQy/o7n/+4N5YE8+eM4jrw6E7cjhpkLJk4WZW30o0uKVcCSQCfu10xYJ9DKnFA0KFaY9mLYHPpBPxwpJKfq5ktKNAv4Ts0A9bxMwcaSajDRJi4+E7J5cJ6s73taWymNeiaYefg8GOVtDCmG7lOpHjukU8D9tnRIAPwytzkcLbFLlnIXp7HoWmRx/1waiqzyCbDqlNkMMZuGHHP3NfAvGDjOixunAzMKN8ziNfs0IXPpi8/cHGTWEJTPxyK1uxhPhvlvEatJmakctNGSKwmdrhasgCwdsrwKpXg8vJZRdBJv2nDqEPxuvz3lezFGq+fIDMuKngd1pkimpS96SIjY1xnunKuunkZjPmLAhh57ugzX7F6B4bgimP/FxAxO8p+GTy+iXA/pGllio53XFfkeSc7Ry3MYenDp2cL18qYfDeRM6Es3Y1so8bq4Uf9ILlF4+GXqpBg8Od0aGKnB19KA9sIusq0bAXnNyFLGbEhl7cuq6V6Mlg9wtRAyrdvXpa9wjcEi+6VQZV1P8OJB525VpcKElA8STTJDyxy9P3kzfxX11doyXyWvqxTyAldRCXjcHrdfCvejol9jEpsKgPeRyxrIBAyzmPLp7eI7/wWSt2ICzLvULLb68ijxzSYAS+rWAnX3krbb/aBBCnHUekyQLB2yFTx6lQDjvm7+xDF/La7dWeu1+9ej5CUt7LZKdRdk+tHDcs17lxMPRzwi6YjAzROjUZKtyMCchyYzxMjeNt63TVZY3FkHWiaFibfRzeHInAx1h3d7jbw3KuYiov2iJ135EYsqWc5CplVuWZtLfxZ1PhMXwGshfblj9tspW9TD97cb8xIwRAN2xDnhzh1seQEsTb2yb83Ugi4acCMvkLyhHyHjhz8xmjCHvwJO8kxJN9bFfF64dapl4E3z2bz2rIVvdXfEG/XEnyc69nGBNE+Bo4G4BQbNw46zGNO4gD/PlJtVym5ms71F297g3/JvQOE4ngPH4ZoCqLOJQxCQLgok8y1cC0kT2PTHezgPXTq+OPyvuWvRblTXoVlJmrTpFMc254YhkPT/v/KyJZuNMcH0PNaaHZrwMsO2ZFmSDUPC/teqRh+jQvrDkYR3r+7tpYnVRrHP5LtlzufJaLzJBpyhVAtTwpdVAZ+dmAvkv6jRWNRFyCiCUrJ7J5e4quOSChh9QBk7dbNShd7MO5ewe18lfAnaZKr0bnfJBjjuBSLmjHAU8Cz9jeTwBgytygrhPmc1Bbd4QkbYMnhYgHYIGmSoRpPH5ZM4fB6GBfg6q/zTfsnQ6hG7IShDoSKMRrXunhDR0aN6gHwRzx6GLHXG6milkfJYncZjRtuW2Fc07soDFaCj/viuvCRCJ2Kk4x/6cTZuUzGp7QS4RoQfd6DGsy5JU+xOEuAMFaUMVT922ndHIit2I3GzduyUtG80S9Bcd2alVaOnkof2VEm6AH+GYGZD+druO+PrtCyKxyJY9b3YaOK7syPhyyrhr0DYws2izdLGzQyD/igvetxJP0yrR+ud59uxg4QnsCY3WRAb0zHQet3O3ZOHNzF2QO/6GtIpGRHPhMZO+kYSJoLup93Op2cTTiSP/XMktUdAo585i8WsWUaYfgd8vZUIV4XAwIGE0bgt/wUSJi82dvimNPNp286xyJdNOLO8OWfjqPyJRjMvvYyp3l5hfEewcWe5ZKhMrrlyYCCctOHTm9+ctYOb1S8QnpxNa5BXTWXiZXB7pU4JdQZFSAh/LRM2YvWz0E4kHAnvxqo4+GoB3pYJA33nF+B8pvvfnC7mP1dzNtDbJYMTPU6T8J2EGHRBNVgyJDwRMaYID/iST8SQ/0P2LxhfZlhJmFa6llkD8w/Oy6NhNs1ip2SgjpmeMmMdWcuJImBSWjADxF5PolMdpH9E9i8b/RgVlUxeIPOyQmyp+lfolJSVuFm7WVdjclRvGv+Rsl7DUEvSWHkBQvx48apChKOmJvguuNSPzgrhooBhX53wZd/KoN4jtzyFzBH8OoUsEFXBmkDY0w4QdMXHmCPOWzbOpBn0cuiwzNeVBczYn3Fs4leeLQh8zXDJ0nNRSbRTOC8SHut3Nov3bGw6v6TryXg7X+/COBoFXI79mc2it2jMqRQQ70LgoFCnU72WMkJ9Ewz/U9zC1y1+sMjDP8NfC3tlAzQefw3oLeO6NE/85WGOh+J7PkmBnzn94wGER7CL3p4bolK77tH8bprfimb4FNHfJ0PVzpzWBXy+ApnzJI2b/e1rUEcUMOE/YLwfPBM3LQ2+L/CKfAOLFU3WukJPnSl3npgiadyVdlSr4BXSJO1uY05bRWwtxxuaAt0ml/ut9mjC0WKtE/6aulm7kTBuxHuECOVGGPsVyph2bYOIz54l7+0yWXLl+lTEICywotHlTokdKL2Jw1BYW+wuEwzTVe/OZuGMN5c9jubI+yy5hQLfom63nV4iePmbEtKp9mpiz7FZE+SMjvTiyZeMbXX4OL3SiWwQLfrytpvpc3OTT9PgB8AGvn7P0N5JGL5tOVLKBg3liJReGUKTgVLyTYRcmU88SzrHGQmRhV4c8M8Z3/4puaznHI92rugMhUtG69OZMQmURfRGW8QCX7CVWSLZLCX6TOKYjVkrReUOuKHFZIm7t3Pjq3FReDNVpck1rNctaBK3MmGm73hemmtkmLDodhz1WbK1CcNLuDp2f7Oe2M86pEfnli5gTH5qC8LbIyWDPG7mZjFHJQ5hktvTh0LResuY32/u0aGjoMViu1QSOayB8udtmAF5IVIyhtms6V0Yin7WfI+fnMpXxkxAlq6Y1uGnEo5SY1fzCrmEOzsOOby00tNMFGkx9rdxpgvvj/Hv6Y3G6mcyZiIp7ZZcN2vBzxcCZmtvaN3gTK/3w+CrGbZQ64mEGQ9oM97P2rCWtAV+y3CH8WocpDHG1zeRFSX8irCBuz2vnHgMV9+vR0rGRDcrNyR0SdKukw7hFE5hM1wVarRSZQo9o4laSs7tvXIJ7PABPPk2YxNOxk4KkZKJMd0Idlc0fAsh0lzEb6sI+c4DHpdLc9/eSBw8d6B6PMTRCbAyrtWZu913dks6a48In9msLIVBv4nNOE14iewYbZ0GHPXrBc7DsaQBw5XWyZLahFOPol3EjVGEAvGwBQquNEft3YKbxUGcrDemKiYyPpXG+Hfgl6pSaDyWXgdpLyOPkhsIGHStTFFZ7ZQ83SwyjmpGm0GVJ3TmD7vGKv5TwoOXIngcYKLe0vD+EC3dLCtY02jtlK7LnRJ6Heo0R1AIaRFUaY5ubEDuZtkrCZNKOU5kh63hv1+JhqXrYTYrHxAySfgzO2VHbyxAO3w9gupU6epa3MwCLjUjgOt6Et0q3AafvnORsEiunL6D4udDLEqYIl6YqiLnweDE0Q2lQShT5RptMg+prY+V3j02sOQvDdvdcwrt4biWkA5zyYxq42yIhRq91BuzATpjw+zVfMYi+Y+iTw5onauIU/e4Lyt3o8sYNlhb9KPPaoPp69GN0u6ZyJsxdd9aHYFeIkyJkiwNtE6mSd1Fos1kmkMztApLp6PUKR14q3kakvlTBlTpqRYHOb9rK2GkS2jmEf7f5sRafi+yvvV3XkEfsiyn70xQVlpvdcAySIZ7P5oj+VKHi/O7SIpX4xZNtGS3jxKqEd3jllAtoblJxBxvT8xmYT6pnIg5XPuktzy76xKE1bK3Ziq9RhngLtmh/ceBjks01GRM4u0y/6bpa+bTyrkdzkWANk6xExcqBY1CnnREy0D+YAMo4uPHZ+WMnV4ejJ/wG4sA27bXB1lxp7S8m0ZJqy9MlMMfvoBPv0wYzRinEPH06lNX94B866YCKwppBHiPxJuX8JOwWKzH+x6K0JeAege6c+17SVq7nvB4/QwpXcIaF88lhjjIWSv7y5D5fG+28mx7hA1Pa9+L6PDYjoH3POW7XzdZZ9pzNwMj+dA7sSKdd5qJ1I+sO1jpBN7LnxeEPXp+fCmbWa5R4cyIWvfxRZERdpZfMp7WpZSQBmYuxmxTPgRnPiQlDZZ4RBmxfAkm4cyCgP7yrOwONdor2kzEQ8WHOElg3toylK5lRyyeHcWJlYR0inn5QpqD0CzcT5A8EMsNWShiHlpmzWF61oIWwbcyJX1eDrtYRyyxITVLN2sbfI5xDACLrmEnNnkY4DV4OStwXhBFHjadjSuUpPVxFy4iZSb7naxL/qJE+P3yMeByueAv4AOfYSF+Pev6+W/i7oVvVwN30JLOSGdDv2E/4N3ItxuOANbXWsarkK3svztqdIkxA9QUMXLl21z++sZbkm8ykpcAO3BgBf/DQnzr5ARfS+mH3Lzzz+HyQ9zzvLr7N85q784CEkNpudrfdeCw71Qn7i2u8PT0sdZBD2EdcAUD6TyjxA2OzDfcENwa4obbBSrJs94k0olJ17au3LXr4wGwskhW41oDMYkeeUwPtHfH3MMqGA3mWA5pj6f3v9oCbu0NX8D3DVs3CAwruuuG6XLQaOHVYBTbhDkOTX8X9ogfwC+8t+zeSkU9OqvVghNxDFlsrHecK1x62/0gNQUb7EUgMyEVnzPc/zE6aON1UE+IDRrt6xZEBkreBME1yFJZIEzoQRInRssNxthwBMVuz8rQq1xHeP/5r8HTMz63y26AmtzEMMPDUtjpeICo3/w1cliVLT8pIF0OIl9VTts9W6GBQMCOqTwwROHqCTFCcZkPQT3BCLBNcJLDFqXm+5c+BsHC0TXoJJQziKy/k2DaK8sWMS1CL0zqBhoNUVVj8w2vDo8jao0Y8wrtVGglw4VIYoF/mNmhuYcyaJWI3cAeoh9kD/YIDK7sQ6USoqMQv6IXaRcg0uQ7BEWEQBDjo0Pz1bSF1TMjsQaWyetpsMRaERw5s10f5+5Qo7djH6HheSQP9hA++OPl/6gAlX+ODot8VmFttLC4XeXWPsKLsACILlBxICWrqAcV9iOMJIW6qD3drDL2r3aT/byv3kvL/37IvBL9KPpH3+OrgCeYRSnGHqz2DmQj4bpRTjokGrPO0ShrHdRo3DrsH+Yu/AzglDfwSFx5q67jfQq3tiGiliVDPSnGhHmceQLR6a5AbRpWQHbCBZxC1tnGIs9Kq+OBTfyIa1Jws8qgfAFl+oF3ag531/btiN9YVtGkJ+jzzMbX2oTxoyIkXzVoEKLwlTqyFtWg1mpgCjzlCnSz/gnRRH31zXlxXBYL/rjwVz/FTlgIqz7WFRirDSLfqO89jsEDMbLzqRqtK7TWcZjohzxTooOd+gBR/T8ZohsfrK2+rcq6q2wkSYj4GxHXeMwilIQM9e4t7l6b5JWUp5TgYUHAocjAHrrNUfAwO/oHPEk0eiLhVYhgms03WomUr8tHWIwdd+yUvM7bUVGRsLtPpqqoVUKRoN50QESjwxDPBqq0vUJUNPfqPP8/pWUCJJiDO2NmRhfZIAzscJwr2EXGLgT40fXgRC1qdIsmIdwZRWxys9hEhagJAlWJ5rkYiz+mIcDg78EGwo/oJKn1jfY2Qo+r/4UyIVJoMKtngIYXWksbY/9RpCKyt03AeawPN4AudrUGj0WrVDm56CTF+eDJtKPoesR9QpgaPUA36GaVOyXxn44JTtP1VZxP5xHv+GDZhg/VE1cjQoY+Cp1KA8DvWhmzY4JHaYLhrjUrYEEYI1BNuILd1ilJfItfDmuJf6Vfr0dJkqMo9SPEkR13rwEdtTeQcS2wbOT3Z483otvYkkIRq8bR6lY4bjd3Srh9QukCsjLs4CeFnj4Fz5bSL7CLIzsaMNJqWy+4Es53HXcghvIoArpAGAuw8Umcn0DOVob/NY5vzuRh43JW11i7HGrGr3CUndKfhvR5LKz8HFlQztj/z0M6qcD8TcY5X3eAdv6ZODizgmuyyq0Cql9/rIDP/j+Aq97/WMKn938RH8NHsfbc7P8BIPnBmKO5uKoAAAAASUVORK5CYII="RS8 KVN">

        <h2>📱 Скачайте Happ</h2>
        <div class="btn-row">
            <a href="https://play.google.com/store/apps/details?id=com.happproxy" target="_blank" class="btn btn-secondary">🤖 Android</a>
            <a href="https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973" target="_blank" class="btn btn-secondary">🍎 iOS</a>
        </div>

        <h2>➕ Добавьте подписку</h2>
        <a href="` + safeHappLink + `" class="btn">📥 Добавить в Happ</a>
        <a href="javascript:void(0)" onclick="copyToClipboard()" class="copy-link">📋 Скопировать ссылку</a>

        <h2>🔌 Нажмите большую кнопку включения</h2>

        <h2>📱 Активируйте в Telegram</h2>
        <a href="` + safeTelegramLink + `" class="btn">🚀 Активировать</a>

        <div class="divider"></div>

        <div class="info">
            <div class="info-row"><span class="info-label">⏱ Срок действия</span><span class="info-value">` + fmt.Sprintf("%d часа", trialHours) + `</span></div>
        </div>

        <p class="note">💎 После активации вы получите подписку без ограничения по времени!</p>
    </div>
    <script>
        function copyToClipboard() {
            var text = '` + safeSubURL + `';
            if (navigator.clipboard && window.isSecureContext) {
                navigator.clipboard.writeText(text).then(function() {
                    alert('Скопировано!');
                }, function(err) {
                    fallbackCopy(text);
                });
            } else {
                fallbackCopy(text);
            }
        }

        function fallbackCopy(text) {
            var textArea = document.createElement("textarea");
            textArea.value = text;
            textArea.style.position = "fixed";
            textArea.style.left = "-9999px";
            document.body.appendChild(textArea);
            textArea.select();
            try {
                document.execCommand('copy');
                alert('Скопировано!');
            } catch (err) {
                alert('Ошибка копирования. Скопируйте ссылку вручную.');
            }
            document.body.removeChild(textArea);
        }
    </script>
</body>
</html>`
	return html
}

func (s *Server) renderErrorPage(message string) string {
	safeMessage := html.EscapeString(message)
	html := `<!DOCTYPE html>
<html lang="ru">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Ошибка</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
        .container { background: white; border-radius: 20px; box-shadow: 0 20px 60px rgba(0,0,0,0.3); padding: 32px; max-width: 400px; width: 100%; text-align: center; }
        h1 { color: #e74c3c; margin-bottom: 20px; }
        p { color: #666; line-height: 1.6; }
    </style>
</head>
<body>
    <div class="container">
        <img class="logo" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAPAAAACGCAMAAADU8Pd8AAAAclBMVEX+/v3////6+/orPEru8fIbLT3d4ePEys2ss7iVnqSBjJQ+TltpdoBVY3ACEiYMIzgKIDUJHzQGHDEIGCpuPiv3hDH2eyP2cS73aSvzYyblazZKLSctHiTJYzWtWTOOSTDdeTllHy2WJzHDLTPlJyvpRizVoAj6AAAY4ElEQVR42tSaC2OiOhOGReQmkJBEbU8BL3X3///FL+9MLuKt0j3f7tmXatEq5GEmk5mhi9+hpdUiIa3yoqyqddO2bU2yO826qsoiXyWkBT78N8vD5kW1bus0k9pAwj60ld0Rwu7KLK2bdVmsPPVfxXgFuyyqps4smLCMqpP31CktrHRWNxVT/12WTkDLli2btANqR1wP5bg1DJ7Va0CzoZPkb/Bihl0V6zoTgli7F0XUgG6rYkmG/itQk2VeWlgtjALBbBG0Stdg/k9O6CWUONSiXLdpZiwsWNVsWqVUh6neGWHS5k8zL+024YRNA2oFVI3oo6QVjT5whFfyevJGVnyEkflPWhhrZ+faf4w6YHrSlUVtyKocngJEYIywSluZqbSOlyF+NDDrusoJGfoD3BHTei+vrp0BqboXneCfCjiKMYdh6PuN1YGEvb4fBuImR1ZQF8S+nbVs5j/i06ty3bS1xcykhk0xUmdUP9Yofqk1SPvT5rDf/xx//vPznwt9WI17y07cWitv5HgMqYSpq9WfQV4u4cC1FGBVkhR4b8xDrED9PP+8o8D+/v7+RtxMDUyazV4wc7rOEcD+H0x4fOHSKwTj2hmZ4Z37MjNgFcMG1giJ7Y4s9dvHeNgMxkNDfETM5q4tCPl3a1oNcDHQ1jBxFEw79J9nhv0xpcXTyNs4fny8kywvQb8RdD+IS2aIPbtcROTfmohNQ3XRpATsYg5May3746fd8BSIabKeesQpp95qYye3BX97Y24wv437DTErKE4RY5GXQP4XDZ0A5UVkny8boUPSEGmtgMy4Z5AO5K0QOGiFoicKaoT9Bmxi3o172FnFYEiebQSszANg/arpVq4ufcW6ucuXpaSAHGmhQHs+b04cgnXMPAAipY/1gstFUAdoMB96upZhMncOeTXJBH4Je7VuytzXpSwgBi1iYsVlnw7Lr9YXtGxhC/t5IlYl2emDAIwyuCoRB+q0c9Sq3+xHYn57f9sdybXjEsDIJk3Tum7x5SJfBuxvErciq5uyWC2Tu1qFdFkIrJoxJkdaCKb9hGW1glHvLtOWD9AlBp3jqJYFrj70BzBDu93Rmtngm7Cxn8sQeYbJ0rapgP1NaPvFBo0IleEKlmVZFLlVUbj2TM2owIgLh76hhW1hWr4krCvc4BZcD+bkN1WbwYmVIeadQ94zcvQQGaXoADJt10Xuy+iZ2EmlNFfjTAafxWFZbDEVFObtFPezHwyuBQE/Fvso14NNkSysB7kgqM2w2R8d8m6/MYZd+o6kGy16B8vZzIk1cpEJKX0fwg1aQg5V6wvYQBuFeau7l8pDHJuglaWs1zl5ZtFkQnRSm/5w3DnmEch3vh8SPFDjspUrMM9067wVWt49NNTxkuJgI23EZU+eJa4TZFNYYNi57YAshs1oke222xLyNBTwnh8UH0JkVEYDmR4vTuQyE528ywtSZgXsrc4nIf1gZjMroVpCXiRFI4WW2gD5kZVlGBse3r19rTErkUpWjRLkwW6iDWowA3Q6WdRr1vOPs5WbwCdDp58tSpo7oZo8wQiSfJ3BU4whZPxs972BBvzEehpS2DiwuVpjBnKSwMhFq0Ro1Ay947wkBaV982Q1sE7wcSL+hoXZX6USWbXkoruS9j3ZwcpvRGyRrUbWHpkq15WGqWPtokW2Xs0qKIGM4GE6SdDagAf6PAHSUxpIQ0Y7bz9tBv1NAzOyFjUt9lLDQ/mYRIwHa7vbOh2P4x7lNEPHlEzjus316ySvagVm2IupjBEk/CZU5fLiy0kOx5ovBoY6Oo0GbMw3d3j4Zy/etdgoN8AcQxhdt1nECZAXxbruhFGkyaiwowkVP9g89Pd4fUUNSSsFWFtRgAiM7/QcNEVm6A0yFIzK5d2iq5LZZWDCdp4IeCR9oXgpoG/y+iKEYWHYN6qdSMfjETPXaxztG9a3PTmYkXtrn5NhcjSvx654D6FqU+UUkLx7mwHioOmaUt9GlhNYrprAytN008czYTYp7gmirDxaVjK2/T0ycufSJdEuZ3RiFznBYjopLxqS4IhsdWZxuI4dyO5LZjVBlb6zGQtj7n64iATBrzqJzQuBBej2S5Ya0LDz8TAYwsUgHhMvr5rrq4sCMKYzOMG9nMN3Nc77T46YXxtZe902AYD64VFdpQK+KBpJlBACuTeYCXkzmI5nCIjBNkH1SibFrvaN9XA3wMP+uC/Xt7IPdOKEfj5hZdanfd/7Lg9QPzwpoQ5AxQDuu3+W4ksQdXn7wVKDmZHHXijpFiixRurmlUCRtKwalKWBFbrpZkBn1oWB4dQHm3bsqSdJq4R6WiVlAPVpBJzXD53bI8/9A+0STGBLCO38cox6g+LXwZiwyJVMvAz3ELj7yHcR4r2hqEgLUEo7hpNPrXwD60wzTbmeOwzVP0XGiq3x+aAQ9/z5nzD7wIlJP7pIjSzEXrMPfjHCrcmXdJaTV6/WdV2n/J8IodSNZg0KtGjYcHsqiv54OqM1+Wlc/OYJebBW2wwPkGXnC1B+QRE1qntJ/j46ysitS8Ngbdb22DsbS9EkAE7yKhVCTU4k49F8aIAVYVfXsJHT8MGh0l4Q69ODVlBcSjfWSQfzwFQy/o7n/+4N5YE8+eM4jrw6E7cjhpkLJk4WZW30o0uKVcCSQCfu10xYJ9DKnFA0KFaY9mLYHPpBPxwpJKfq5ktKNAv4Ts0A9bxMwcaSajDRJi4+E7J5cJ6s73taWymNeiaYefg8GOVtDCmG7lOpHjukU8D9tnRIAPwytzkcLbFLlnIXp7HoWmRx/1waiqzyCbDqlNkMMZuGHHP3NfAvGDjOixunAzMKN8ziNfs0IXPpi8/cHGTWEJTPxyK1uxhPhvlvEatJmakctNGSKwmdrhasgCwdsrwKpXg8vJZRdBJv2nDqEPxuvz3lezFGq+fIDMuKngd1pkimpS96SIjY1xnunKuunkZjPmLAhh57ugzX7F6B4bgimP/FxAxO8p+GTy+iXA/pGllio53XFfkeSc7Ry3MYenDp2cL18qYfDeRM6Es3Y1so8bq4Uf9ILlF4+GXqpBg8Od0aGKnB19KA9sIusq0bAXnNyFLGbEhl7cuq6V6Mlg9wtRAyrdvXpa9wjcEi+6VQZV1P8OJB525VpcKElA8STTJDyxy9P3kzfxX11doyXyWvqxTyAldRCXjcHrdfCvejol9jEpsKgPeRyxrIBAyzmPLp7eI7/wWSt2ICzLvULLb68ijxzSYAS+rWAnX3krbb/aBBCnHUekyQLB2yFTx6lQDjvm7+xDF/La7dWeu1+9ej5CUt7LZKdRdk+tHDcs17lxMPRzwi6YjAzROjUZKtyMCchyYzxMjeNt63TVZY3FkHWiaFibfRzeHInAx1h3d7jbw3KuYiov2iJ135EYsqWc5CplVuWZtLfxZ1PhMXwGshfblj9tspW9TD97cb8xIwRAN2xDnhzh1seQEsTb2yb83Ugi4acCMvkLyhHyHjhz8xmjCHvwJO8kxJN9bFfF64dapl4E3z2bz2rIVvdXfEG/XEnyc69nGBNE+Bo4G4BQbNw46zGNO4gD/PlJtVym5ms71F297g3/JvQOE4ngPH4ZoCqLOJQxCQLgok8y1cC0kT2PTHezgPXTq+OPyvuWvRblTXoVlJmrTpFMc254YhkPT/v/KyJZuNMcH0PNaaHZrwMsO2ZFmSDUPC/teqRh+jQvrDkYR3r+7tpYnVRrHP5LtlzufJaLzJBpyhVAtTwpdVAZ+dmAvkv6jRWNRFyCiCUrJ7J5e4quOSChh9QBk7dbNShd7MO5ewe18lfAnaZKr0bnfJBjjuBSLmjHAU8Cz9jeTwBgytygrhPmc1Bbd4QkbYMnhYgHYIGmSoRpPH5ZM4fB6GBfg6q/zTfsnQ6hG7IShDoSKMRrXunhDR0aN6gHwRzx6GLHXG6milkfJYncZjRtuW2Fc07soDFaCj/viuvCRCJ2Kk4x/6cTZuUzGp7QS4RoQfd6DGsy5JU+xOEuAMFaUMVT922ndHIit2I3GzduyUtG80S9Bcd2alVaOnkof2VEm6AH+GYGZD+druO+PrtCyKxyJY9b3YaOK7syPhyyrhr0DYws2izdLGzQyD/igvetxJP0yrR+ud59uxg4QnsCY3WRAb0zHQet3O3ZOHNzF2QO/6GtIpGRHPhMZO+kYSJoLup93Op2cTTiSP/XMktUdAo585i8WsWUaYfgd8vZUIV4XAwIGE0bgt/wUSJi82dvimNPNp286xyJdNOLO8OWfjqPyJRjMvvYyp3l5hfEewcWe5ZKhMrrlyYCCctOHTm9+ctYOb1S8QnpxNa5BXTWXiZXB7pU4JdQZFSAh/LRM2YvWz0E4kHAnvxqo4+GoB3pYJA33nF+B8pvvfnC7mP1dzNtDbJYMTPU6T8J2EGHRBNVgyJDwRMaYID/iST8SQ/0P2LxhfZlhJmFa6llkD8w/Oy6NhNs1ip2SgjpmeMmMdWcuJImBSWjADxF5PolMdpH9E9i8b/RgVlUxeIPOyQmyp+lfolJSVuFm7WVdjclRvGv+Rsl7DUEvSWHkBQvx48apChKOmJvguuNSPzgrhooBhX53wZd/KoN4jtzyFzBH8OoUsEFXBmkDY0w4QdMXHmCPOWzbOpBn0cuiwzNeVBczYn3Fs4leeLQh8zXDJ0nNRSbRTOC8SHut3Nov3bGw6v6TryXg7X+/COBoFXI79mc2it2jMqRQQ70LgoFCnU72WMkJ9Ewz/U9zC1y1+sMjDP8NfC3tlAzQefw3oLeO6NE/85WGOh+J7PkmBnzn94wGER7CL3p4bolK77tH8bprfimb4FNHfJ0PVzpzWBXy+ApnzJI2b/e1rUEcUMOE/YLwfPBM3LQ2+L/CKfAOLFU3WukJPnSl3npgiadyVdlSr4BXSJO1uY05bRWwtxxuaAt0ml/ut9mjC0WKtE/6aulm7kTBuxHuECOVGGPsVyph2bYOIz54l7+0yWXLl+lTEICywotHlTokdKL2Jw1BYW+wuEwzTVe/OZuGMN5c9jubI+yy5hQLfom63nV4iePmbEtKp9mpiz7FZE+SMjvTiyZeMbXX4OL3SiWwQLfrytpvpc3OTT9PgB8AGvn7P0N5JGL5tOVLKBg3liJReGUKTgVLyTYRcmU88SzrHGQmRhV4c8M8Z3/4puaznHI92rugMhUtG69OZMQmURfRGW8QCX7CVWSLZLCX6TOKYjVkrReUOuKHFZIm7t3Pjq3FReDNVpck1rNctaBK3MmGm73hemmtkmLDodhz1WbK1CcNLuDp2f7Oe2M86pEfnli5gTH5qC8LbIyWDPG7mZjFHJQ5hktvTh0LResuY32/u0aGjoMViu1QSOayB8udtmAF5IVIyhtms6V0Yin7WfI+fnMpXxkxAlq6Y1uGnEo5SY1fzCrmEOzsOOby00tNMFGkx9rdxpgvvj/Hv6Y3G6mcyZiIp7ZZcN2vBzxcCZmtvaN3gTK/3w+CrGbZQ64mEGQ9oM97P2rCWtAV+y3CH8WocpDHG1zeRFSX8irCBuz2vnHgMV9+vR0rGRDcrNyR0SdKukw7hFE5hM1wVarRSZQo9o4laSs7tvXIJ7PABPPk2YxNOxk4KkZKJMd0Idlc0fAsh0lzEb6sI+c4DHpdLc9/eSBw8d6B6PMTRCbAyrtWZu913dks6a48In9msLIVBv4nNOE14iewYbZ0GHPXrBc7DsaQBw5XWyZLahFOPol3EjVGEAvGwBQquNEft3YKbxUGcrDemKiYyPpXG+Hfgl6pSaDyWXgdpLyOPkhsIGHStTFFZ7ZQ83SwyjmpGm0GVJ3TmD7vGKv5TwoOXIngcYKLe0vD+EC3dLCtY02jtlK7LnRJ6Heo0R1AIaRFUaY5ubEDuZtkrCZNKOU5kh63hv1+JhqXrYTYrHxAySfgzO2VHbyxAO3w9gupU6epa3MwCLjUjgOt6Et0q3AafvnORsEiunL6D4udDLEqYIl6YqiLnweDE0Q2lQShT5RptMg+prY+V3j02sOQvDdvdcwrt4biWkA5zyYxq42yIhRq91BuzATpjw+zVfMYi+Y+iTw5onauIU/e4Lyt3o8sYNlhb9KPPaoPp69GN0u6ZyJsxdd9aHYFeIkyJkiwNtE6mSd1Fos1kmkMztApLp6PUKR14q3kakvlTBlTpqRYHOb9rK2GkS2jmEf7f5sRafi+yvvV3XkEfsiyn70xQVlpvdcAySIZ7P5oj+VKHi/O7SIpX4xZNtGS3jxKqEd3jllAtoblJxBxvT8xmYT6pnIg5XPuktzy76xKE1bK3Ziq9RhngLtmh/ceBjks01GRM4u0y/6bpa+bTyrkdzkWANk6xExcqBY1CnnREy0D+YAMo4uPHZ+WMnV4ejJ/wG4sA27bXB1lxp7S8m0ZJqy9MlMMfvoBPv0wYzRinEPH06lNX94B866YCKwppBHiPxJuX8JOwWKzH+x6K0JeAege6c+17SVq7nvB4/QwpXcIaF88lhjjIWSv7y5D5fG+28mx7hA1Pa9+L6PDYjoH3POW7XzdZZ9pzNwMj+dA7sSKdd5qJ1I+sO1jpBN7LnxeEPXp+fCmbWa5R4cyIWvfxRZERdpZfMp7WpZSQBmYuxmxTPgRnPiQlDZZ4RBmxfAkm4cyCgP7yrOwONdor2kzEQ8WHOElg3toylK5lRyyeHcWJlYR0inn5QpqD0CzcT5A8EMsNWShiHlpmzWF61oIWwbcyJX1eDrtYRyyxITVLN2sbfI5xDACLrmEnNnkY4DV4OStwXhBFHjadjSuUpPVxFy4iZSb7naxL/qJE+P3yMeByueAv4AOfYSF+Pev6+W/i7oVvVwN30JLOSGdDv2E/4N3ItxuOANbXWsarkK3svztqdIkxA9QUMXLl21z++sZbkm8ykpcAO3BgBf/DQnzr5ARfS+mH3Lzzz+HyQ9zzvLr7N85q784CEkNpudrfdeCw71Qn7i2u8PT0sdZBD2EdcAUD6TyjxA2OzDfcENwa4obbBSrJs94k0olJ17au3LXr4wGwskhW41oDMYkeeUwPtHfH3MMqGA3mWA5pj6f3v9oCbu0NX8D3DVs3CAwruuuG6XLQaOHVYBTbhDkOTX8X9ogfwC+8t+zeSkU9OqvVghNxDFlsrHecK1x62/0gNQUb7EUgMyEVnzPc/zE6aON1UE+IDRrt6xZEBkreBME1yFJZIEzoQRInRssNxthwBMVuz8rQq1xHeP/5r8HTMz63y26AmtzEMMPDUtjpeICo3/w1cliVLT8pIF0OIl9VTts9W6GBQMCOqTwwROHqCTFCcZkPQT3BCLBNcJLDFqXm+5c+BsHC0TXoJJQziKy/k2DaK8sWMS1CL0zqBhoNUVVj8w2vDo8jao0Y8wrtVGglw4VIYoF/mNmhuYcyaJWI3cAeoh9kD/YIDK7sQ6USoqMQv6IXaRcg0uQ7BEWEQBDjo0Pz1bSF1TMjsQaWyetpsMRaERw5s10f5+5Qo7djH6HheSQP9hA++OPl/6gAlX+ODot8VmFttLC4XeXWPsKLsACILlBxICWrqAcV9iOMJIW6qD3drDL2r3aT/byv3kvL/37IvBL9KPpH3+OrgCeYRSnGHqz2DmQj4bpRTjokGrPO0ShrHdRo3DrsH+Yu/AzglDfwSFx5q67jfQq3tiGiliVDPSnGhHmceQLR6a5AbRpWQHbCBZxC1tnGIs9Kq+OBTfyIa1Jws8qgfAFl+oF3ag531/btiN9YVtGkJ+jzzMbX2oTxoyIkXzVoEKLwlTqyFtWg1mpgCjzlCnSz/gnRRH31zXlxXBYL/rjwVz/FTlgIqz7WFRirDSLfqO89jsEDMbLzqRqtK7TWcZjohzxTooOd+gBR/T8ZohsfrK2+rcq6q2wkSYj4GxHXeMwilIQM9e4t7l6b5JWUp5TgYUHAocjAHrrNUfAwO/oHPEk0eiLhVYhgms03WomUr8tHWIwdd+yUvM7bUVGRsLtPpqqoVUKRoN50QESjwxDPBqq0vUJUNPfqPP8/pWUCJJiDO2NmRhfZIAzscJwr2EXGLgT40fXgRC1qdIsmIdwZRWxys9hEhagJAlWJ5rkYiz+mIcDg78EGwo/oJKn1jfY2Qo+r/4UyIVJoMKtngIYXWksbY/9RpCKyt03AeawPN4AudrUGj0WrVDm56CTF+eDJtKPoesR9QpgaPUA36GaVOyXxn44JTtP1VZxP5xHv+GDZhg/VE1cjQoY+Cp1KA8DvWhmzY4JHaYLhrjUrYEEYI1BNuILd1ilJfItfDmuJf6Vfr0dJkqMo9SPEkR13rwEdtTeQcS2wbOT3Z483otvYkkIRq8bR6lY4bjd3Srh9QukCsjLs4CeFnj4Fz5bSL7CLIzsaMNJqWy+4Es53HXcghvIoArpAGAuw8Umcn0DOVob/NY5vzuRh43JW11i7HGrGr3CUndKfhvR5LKz8HFlQztj/z0M6qcD8TcY5X3eAdv6ZODizgmuyyq0Cql9/rIDP/j+Aq97/WMKn938RH8NHsfbc7P8BIPnBmKO5uKoAAAAASUVORK5CYII="RS8 KVN">
        <h1>Ошибка</h1>
        <p>` + safeMessage + `</p>
    </div>
</body>
</html>`
	return html
}

func getClientIP(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		ips := strings.Split(forwarded, ",")
		if len(ips) > 0 {
			ip := strings.TrimSpace(ips[0])
			if ip != "" {
				return ip
			}
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return ip
}
