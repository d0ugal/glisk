package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d0ugal/glisk/internal/scan"
)

// multiVols is a VolumeSet with several volumes for switcher/metrics tests.
type multiVols struct {
	infos []scan.VolumeInfo
	scs   map[string]Scanner
}

func (m multiVols) Volumes() []scan.VolumeInfo { return m.infos }
func (m multiVols) Default() string {
	if len(m.infos) == 0 {
		return ""
	}

	return m.infos[0].ID
}
func (m multiVols) Scanner(id string) (Scanner, bool) {
	s, ok := m.scs[id]
	return s, ok
}

func newMulti(t *testing.T) *Server {
	t.Setenv("GLISK_PASSWORD", "pw")

	a := &fakeScanner{status: scan.Status{ID: "a", HasData: true, TotalBytes: 10}}
	b := &fakeScanner{status: scan.Status{ID: "b", HasData: true, TotalBytes: 20}}
	vs := multiVols{
		infos: []scan.VolumeInfo{
			{ID: "a", Label: "/a", Status: a.Status()},
			{ID: "b", Label: "/b", Status: b.Status()},
		},
		scs: map[string]Scanner{"a": a, "b": b},
	}

	return New(vs, nil)
}

func TestLoginGetServesPage(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "glisk") {
		t.Error("login page should mention glisk")
	}
}

func TestLoginPostWrongShowsError(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong login = %d, want 401", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "Incorrect password") {
		t.Error("expected an error banner on the re-served login page")
	}
}

func TestRescanFolderBadJSON(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	req := authReq(h, httptest.NewRequest(http.MethodPost, "/api/rescan-folder", strings.NewReader("{not json")))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
}

func TestRescanFolderConflict(t *testing.T) {
	t.Setenv("GLISK_PASSWORD", "pw")

	fs := &fakeScanner{rescanError: errStub("a full scan is in progress")}
	h := New(fakeVolumes{id: "v", sc: fs}, nil)
	rec := httptest.NewRecorder()
	req := authReq(h, httptest.NewRequest(http.MethodPost, "/api/rescan-folder", strings.NewReader(`{"segments":["x"]}`)))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict = %d, want 409", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "a full scan is in progress") {
		t.Errorf("expected error message in body, got %s", rec.Body.String())
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

func TestUnknownVolumeAcrossEndpoints(t *testing.T) {
	_, h := newTestServer(t)

	for _, path := range []string{"/api/tree?vol=nope", "/api/status?vol=nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, path, nil)))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	// rescan + rescan-folder POST with unknown vol
	for _, path := range []string{"/api/rescan?vol=nope", "/api/rescan-folder?vol=nope"} {
		rec := httptest.NewRecorder()
		body := strings.NewReader(`{"segments":["x"]}`)
		h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodPost, path, body)))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestStaticServedWhenAuthed(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	// Authed request to a non-API path exercises handleStatic (SPA fallback).
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)))

	if rec.Code == http.StatusUnauthorized {
		t.Errorf("authed SPA route should not be 401, got %d", rec.Code)
	}
}

func TestMetricsLabelsEachVolume(t *testing.T) {
	h := newMulti(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	for _, want := range []string{
		`glisk_total_bytes{volume="a"} 10`,
		`glisk_total_bytes{volume="b"} 20`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestVolumesListsAll(t *testing.T) {
	h := newMulti(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/volumes", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("/api/volumes = %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"id":"a"`) || !strings.Contains(body, `"id":"b"`) {
		t.Errorf("expected both volumes, got %s", body)
	}
}

func TestRescanFolderRequiresPost(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/api/rescan-folder", nil)))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/rescan-folder = %d, want 405", rec.Code)
	}
}

func TestStaticServesRealAsset(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	// .gitkeep exists in the embedded dist, exercising the real-asset branch.
	h.ServeHTTP(rec, authReq(h, httptest.NewRequest(http.MethodGet, "/.gitkeep", nil)))

	if rec.Code != http.StatusOK {
		t.Errorf("GET /.gitkeep = %d, want 200", rec.Code)
	}
}

func TestHealthBody(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("health = %d %q", rec.Code, rec.Body.String())
	}
}
