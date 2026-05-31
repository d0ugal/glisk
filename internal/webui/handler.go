// Package webui exposes the scan results over HTTP and serves the embedded SPA.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/d0ugal/glisk/internal/scan"
)

// Scanner is the subset of *scan.Scanner a single volume exposes.
type Scanner interface {
	Status() scan.Status
	Result() scan.Result
	Trigger() bool
	RescanSubtree(ctx context.Context, segments []string) error
}

// VolumeSet is the multi-volume surface the handler depends on. Scanner
// returns the per-volume Scanner interface so it can be faked in tests.
type VolumeSet interface {
	Volumes() []scan.VolumeInfo
	Scanner(id string) (Scanner, bool)
	Default() string
}

// sessionDuration keeps a login valid for 30 days — a once-a-month prompt for
// a homelab device, while still rotating tokens.
const sessionDuration = 30 * 24 * time.Hour

// Server wires the volume set up to HTTP routes.
type Server struct {
	vols   VolumeSet
	log    *slog.Logger
	static http.Handler
	mux    http.Handler

	password  string
	sessions  map[string]time.Time
	sessionMu sync.RWMutex
}

// New builds the web server. Like frigate-archive, the UI password is read
// from the environment (GLISK_PASSWORD) and is mandatory — New panics if it
// is unset. The SPA and /api routes require a session; /health and /metrics
// stay open for Docker and Prometheus.
func New(vols VolumeSet, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}

	password := os.Getenv("GLISK_PASSWORD")
	if password == "" {
		log.Error("GLISK_PASSWORD environment variable not set")
		panic("GLISK_PASSWORD must be set")
	}

	log.Info("web UI authentication enabled")

	s := &Server{
		vols:     vols,
		log:      log,
		static:   http.FileServer(http.FS(frontendDist())),
		password: password,
		sessions: make(map[string]time.Time),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/api/volumes", s.requireAuth(s.handleVolumes))
	mux.HandleFunc("/api/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("/api/tree", s.requireAuth(s.handleTree))
	mux.HandleFunc("/api/rescan", s.requireAuth(s.handleRescan))
	mux.HandleFunc("/api/rescan-folder", s.requireAuth(s.handleRescanFolder))
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", s.requireAuth(s.handleStatic))
	s.mux = mux

	return s
}

// volume resolves the ?vol= query parameter (defaulting to the first volume).
// It writes a 404 and returns false if the id is unknown.
func (s *Server) volume(w http.ResponseWriter, r *http.Request) (Scanner, bool) {
	id := r.URL.Query().Get("vol")
	if id == "" {
		id = s.vols.Default()
	}

	sc, ok := s.vols.Scanner(id)
	if !ok {
		http.Error(w, "unknown volume", http.StatusNotFound)
		return nil, false
	}

	return sc, true
}

// ServeHTTP lets *Server satisfy http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// requireAuth gates a handler behind a valid session cookie. API paths get a
// 401; everything else is redirected to the login page.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || !s.validSession(cookie.Value) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}

			return
		}

		next(w, r)
	}
}

func (s *Server) validSession(token string) bool {
	s.sessionMu.RLock()
	defer s.sessionMu.RUnlock()

	exp, ok := s.sessions[token]

	return ok && time.Now().Before(exp)
}

func (s *Server) createSession() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.URLEncoding.EncodeToString(b)

	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	s.sessions[token] = time.Now().Add(sessionDuration)
	for t, exp := range s.sessions { // opportunistic cleanup
		if time.Now().After(exp) {
			delete(s.sessions, t)
		}
	}

	return token
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(s.password)) == 1 {
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    s.createSession(),
				Path:     "/",
				MaxAge:   int(sessionDuration.Seconds()),
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)

			return
		}

		w.WriteHeader(http.StatusUnauthorized)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	failed := r.Method == http.MethodPost
	_, _ = w.Write([]byte(strings.Replace(loginHTML, "<!--ERR-->", loginError(failed), 1)))
}

func loginError(failed bool) string {
	if failed {
		return `<p class="err">Incorrect password</p>`
	}

	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleVolumes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.vols.Volumes())
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.volume(w, r)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, sc.Status())
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.volume(w, r)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, sc.Result())
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sc, ok := s.volume(w, r)
	if !ok {
		return
	}

	queued := sc.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": queued})
}

// handleRescanFolder re-walks one folder and returns the updated tree. Used by
// the UI's per-folder "rescan" button while the user is optimising a folder.
func (s *Server) handleRescanFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sc, ok := s.volume(w, r)
	if !ok {
		return
	}

	var body struct {
		Segments []string `json:"segments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if err := sc.RescanSubtree(r.Context(), body.Segments); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, sc.Result())
}

// handleMetrics emits a small Prometheus text-format exposition. Kept
// dependency-free on purpose; the values mirror the scan status.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	vols := s.vols.Volumes()

	var b strings.Builder

	header := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	b01 := func(v bool) int {
		if v {
			return 1
		}

		return 0
	}
	emit := func(name, help string, val func(scan.Status) any) {
		header(name, help, "gauge")

		for _, v := range vols {
			fmt.Fprintf(&b, "%s{volume=%q} %v\n", name, v.ID, val(v.Status))
		}
	}
	emit("glisk_total_bytes", "Total bytes under the scanned root.", func(s scan.Status) any { return s.TotalBytes })
	emit("glisk_files_total", "Number of files seen in the last scan.", func(s scan.Status) any { return s.Files })
	emit("glisk_dirs_total", "Number of directories seen in the last scan.", func(s scan.Status) any { return s.Dirs })
	emit("glisk_scan_duration_seconds", "Duration of the last completed scan.", func(s scan.Status) any { return s.DurationSec })
	emit("glisk_last_scan_timestamp_seconds", "Unix time of the last completed scan.", func(s scan.Status) any { return s.LastScanUnix })
	emit("glisk_next_scan_timestamp_seconds", "Unix time of the next scheduled scan.", func(s scan.Status) any { return s.NextScanUnix })
	emit("glisk_scanning", "1 while a scan is in progress, else 0.", func(s scan.Status) any { return b01(s.Scanning) })
	emit("glisk_has_data", "1 once at least one scan has completed.", func(s scan.Status) any { return b01(s.HasData) })

	_, _ = w.Write([]byte(b.String()))
}

// loginHTML is the standalone sign-in page, styled to match the SPA. The
// single %s is an optional error banner.
const loginHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="theme-color" content="#070809">
<title>glisk · sign in</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght@9..144,300;9..144,600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: 'JetBrains Mono', ui-monospace, monospace;
  background:
    radial-gradient(1000px 700px at 30% 20%, rgba(232,183,106,0.06), transparent 60%),
    radial-gradient(800px 600px at 75% 85%, rgba(86,130,196,0.07), transparent 55%),
    #070809;
  color: #f4efe6;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card {
  width: 100%;
  max-width: 360px;
  padding: 2.5rem;
  text-align: center;
}
h1 {
  font-family: 'Fraunces', Georgia, serif;
  font-weight: 300;
  font-size: 2rem;
  letter-spacing: -0.02em;
}
h1 span { font-weight: 600; font-style: italic; color: #e8b76a; }
p.sub {
  color: #5f5c57;
  font-size: 0.72rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  margin: 0.5rem 0 2rem;
}
input {
  width: 100%;
  padding: 0.8rem 1rem;
  background: rgba(255,255,255,0.03);
  border: 1px solid rgba(255,255,255,0.1);
  border-radius: 10px;
  color: #f4efe6;
  font: inherit;
  text-align: center;
  letter-spacing: 0.1em;
}
input:focus { outline: none; border-color: #e8b76a; }
button {
  width: 100%;
  margin-top: 0.9rem;
  padding: 0.8rem;
  background: rgba(232,183,106,0.16);
  border: 1px solid rgba(232,183,106,0.4);
  border-radius: 999px;
  color: #e8b76a;
  font: inherit;
  text-transform: uppercase;
  letter-spacing: 0.1em;
  cursor: pointer;
  transition: background 0.2s ease;
}
button:hover { background: rgba(232,183,106,0.26); }
.err { color: #e06a5a; font-size: 0.8rem; margin-bottom: 1rem; }
</style>
</head>
<body>
<form class="card" method="post" action="/login">
  <h1>gl<span>isk</span></h1>
  <p class="sub">a glance at your disk</p>
  <!--ERR-->
  <input type="password" name="password" placeholder="password" autofocus autocomplete="current-password">
  <button type="submit">enter</button>
</form>
</body>
</html>`

// handleStatic serves the embedded SPA, falling back to index.html so client
// routing works for any non-asset path.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean == "" {
		clean = "index.html"
	}

	if _, err := fs.Stat(frontendDist(), clean); err != nil {
		// Not a real asset — hand back the SPA shell.
		r.URL.Path = "/"
	}

	s.static.ServeHTTP(w, r)
}
