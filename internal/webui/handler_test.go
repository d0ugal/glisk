package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d0ugal/glisk/internal/scan"
)

type fakeScanner struct {
	status      scan.Status
	result      scan.Result
	triggered   bool
	rescanned   []string
	rescanError error
}

func (f *fakeScanner) Status() scan.Status { return f.status }
func (f *fakeScanner) Result() scan.Result { return f.result }
func (f *fakeScanner) Trigger() bool {
	f.triggered = true
	return true
}
func (f *fakeScanner) RescanSubtree(_ context.Context, segments []string) error {
	f.rescanned = segments
	return f.rescanError
}

// fakeVolumes is a single-volume VolumeSet backed by a fakeScanner.
type fakeVolumes struct {
	id string
	sc *fakeScanner
}

func (v fakeVolumes) Volumes() []scan.VolumeInfo {
	return []scan.VolumeInfo{{ID: v.id, Label: "/" + v.id, Status: v.sc.Status()}}
}
func (v fakeVolumes) Default() string { return v.id }
func (v fakeVolumes) Scanner(id string) (Scanner, bool) {
	if id == v.id {
		return v.sc, true
	}
	return nil, false
}

// authReq adds a valid session cookie so a request passes requireAuth.
func authReq(s *Server, req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: "session", Value: s.createSession()})
	return req
}

func newTestServer(t *testing.T) (*fakeScanner, *Server) {
	fs := &fakeScanner{
		status: scan.Status{HasData: true, TotalBytes: 1234, Root: "/volume1"},
		result: scan.Result{
			Status: scan.Status{HasData: true, TotalBytes: 1234, Root: "/volume1"},
			Tree:   &scan.Node{Name: "/volume1", Size: 1234, Dir: true},
		},
	}
	t.Setenv("GLISK_PASSWORD", "test-pass")
	return fs, New(fakeVolumes{id: "volume1", sc: fs}, nil)
}

func TestStatusEndpoint(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/status", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	var st scan.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.TotalBytes != 1234 || st.Root != "/volume1" {
		t.Errorf("unexpected status: %+v", st)
	}
}

func TestTreeEndpoint(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/tree", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	var res scan.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Tree == nil || res.Tree.Name != "/volume1" {
		t.Errorf("unexpected tree: %+v", res.Tree)
	}
}

func TestRescanRequiresPost(t *testing.T) {
	fs, h := newTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/rescan", nil)))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/rescan = %d, want 405", rec.Code)
	}
	if fs.triggered {
		t.Error("GET should not trigger a scan")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodPost, "/api/rescan", nil)))
	if rec.Code != http.StatusAccepted {
		t.Errorf("POST /api/rescan = %d, want 202", rec.Code)
	}
	if !fs.triggered {
		t.Error("POST should trigger a scan")
	}
}

func TestRescanFolderEndpoint(t *testing.T) {
	fs, h := newTestServer(t)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"segments":["nas","Media"]}`)
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodPost, "/api/rescan-folder", body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rescan-folder = %d, want 200", rec.Code)
	}
	if len(fs.rescanned) != 2 || fs.rescanned[0] != "nas" || fs.rescanned[1] != "Media" {
		t.Errorf("RescanSubtree got %v, want [nas Media]", fs.rescanned)
	}
}

func TestVolumesEndpoint(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/volumes", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/volumes = %d, want 200", rec.Code)
	}
	var vols []scan.VolumeInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &vols); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(vols) != 1 || vols[0].ID != "volume1" || vols[0].Label != "/volume1" {
		t.Errorf("unexpected volumes: %+v", vols)
	}
}

func TestUnknownVolume404(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/tree?vol=nope", nil)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown vol = %d, want 404", rec.Code)
	}
}

func TestAuthGatesRoutes(t *testing.T) {
	t.Setenv("GLISK_PASSWORD", "hunter2")
	fs := &fakeScanner{result: scan.Result{Tree: &scan.Node{Name: "/volume1"}}}
	h := New(fakeVolumes{id: "volume1", sc: fs}, nil)

	// Unauthenticated API call → 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tree", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /api/tree = %d, want 401", rec.Code)
	}

	// Unauthenticated page → redirect to /login.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("unauth / = %d loc=%q, want 303 → /login", rec.Code, rec.Header().Get("Location"))
	}

	// Wrong password → 401, no cookie.
	rec = httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=nope"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", rec.Code)
	}

	// Correct password → cookie that then authorises the API.
	rec = httptest.NewRecorder()
	ok := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=hunter2"))
	ok.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, ok)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a session cookie after correct login")
	}

	rec = httptest.NewRecorder()
	authed := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
	authed.AddCookie(cookies[0])
	h.ServeHTTP(rec, authed)
	if rec.Code != http.StatusOK {
		t.Fatalf("authed /api/tree = %d, want 200", rec.Code)
	}
}

func TestHealthAndMetricsStayOpen(t *testing.T) {
	t.Setenv("GLISK_PASSWORD", "hunter2")
	fs := &fakeScanner{}
	h := New(fakeVolumes{id: "volume1", sc: fs}, nil)
	for _, path := range []string{"/health", "/metrics"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s with auth on = %d, want 200 (must stay open)", path, rec.Code)
		}
	}
}

func TestMetricsEndpoint(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`glisk_total_bytes{volume="volume1"} 1234`,
		`glisk_has_data{volume="volume1"} 1`,
		`glisk_scanning{volume="volume1"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
}
