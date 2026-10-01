package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lee101/offsitecalculator/appnz"
	"github.com/lee101/offsitecalculator/offsite"
)

type Config struct {
	BaseURL        string
	AppnzBaseURL   string
	ClientID       string
	SessionSecret  string
	MapsServerKey  string
	MapsBrowserKey string
	Model          string
	Web            fs.FS
}

func ConfigFromEnv(web fs.FS) Config {
	env := func(k, d string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return d
	}
	mapsKey := env("GOOGLE_MAPS_API_KEY", "")
	return Config{
		BaseURL:        strings.TrimRight(env("BASE_URL", env("APPCHAT_PUBLIC_URL", "")), "/"),
		AppnzBaseURL:   env("APPNZ_BASE_URL", appnz.DefaultBaseURL),
		ClientID:       env("APPNZ_CLIENT_ID", ""),
		SessionSecret:  env("SESSION_SECRET", ""),
		MapsServerKey:  mapsKey,
		MapsBrowserKey: env("GOOGLE_MAPS_BROWSER_KEY", mapsKey),
		Model:          env("OFFSITE_MODEL", "gpt-5.5"),
		Web:            web,
	}
}

type Server struct {
	cfg    Config
	db     *offsite.DB
	geo    *offsite.Geocoder
	app    *appnz.Client
	sealer *appnz.Sealer
	limit  *limiter
}

func New(cfg Config) (*Server, error) {
	if cfg.SessionSecret == "" {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		cfg.SessionSecret = hex.EncodeToString(b)
		log.Printf("SESSION_SECRET unset: sessions will not survive a restart")
	}
	sealer, err := appnz.NewSealer(cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	app := appnz.New(cfg.ClientID)
	app.BaseURL = cfg.AppnzBaseURL
	db := offsite.Default()
	return &Server{
		cfg: cfg, db: db, app: app, sealer: sealer, limit: newLimiter(60, time.Minute),
		geo: &offsite.Geocoder{DB: db, APIKey: cfg.MapsServerKey},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/solve", s.handleSolve)
	mux.HandleFunc("POST /api/geocode", s.handleGeocode)
	mux.HandleFunc("GET /api/airports", s.handleAirports)
	mux.HandleFunc("GET /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/callback", s.handleCallback)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/agent", s.handleAgent)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	if s.cfg.Web != nil {
		mux.Handle("/", http.FileServerFS(s.cfg.Web))
	}
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeErr(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) origin(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	proto := "http"
	if r.TLS != nil || strings.EqualFold(firstField(r.Header.Get("X-Forwarded-Proto")), "https") {
		proto = "https"
	}
	host := firstField(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}

func firstField(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"mapsKey":      s.cfg.MapsBrowserKey,
		"agentEnabled": s.cfg.ClientID != "",
		"model":        s.cfg.Model,
		"appnzBase":    s.app.BaseURL,
	})
}

type solveRequest struct {
	People      []offsite.Person    `json:"people"`
	Constraints offsite.Constraints `json:"constraints"`
}

func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	var req solveRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.People) > 200 {
		writeErr(w, 400, "at most 200 people")
		return
	}
	res, err := s.db.Solve(req.People, req.Constraints)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleGeocode(w http.ResponseWriter, r *http.Request) {
	if !s.limit.allow(clientIP(r)) {
		writeErr(w, 429, "too many lookups, slow down")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	places := offsite.ParseLines(req.Text)
	if len(places) > 100 {
		writeErr(w, 400, "at most 100 locations per paste")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, 200, map[string]any{"places": s.geo.Resolve(ctx, places)})
}

func (s *Server) handleAirports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeJSON(w, 200, map[string]any{"airports": s.db.Airports})
}

func clientIP(r *http.Request) string {
	if ip := firstField(r.Header.Get("X-Forwarded-For")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{key: l.hits[key]}
	}
	return true
}

var errNotSignedIn = errors.New("sign in with app.nz")
