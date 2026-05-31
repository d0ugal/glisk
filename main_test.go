package main

import (
	"testing"
	"time"

	"github.com/d0ugal/glisk/internal/scan"
)

func TestEnvHelpers(t *testing.T) {
	if env("E_MISSING", "def") != "def" {
		t.Error("env missing should return default")
	}

	t.Setenv("E_SET", "hello")

	if env("E_SET", "def") != "hello" {
		t.Error("env set should return value")
	}

	t.Setenv("I_OK", "42")
	t.Setenv("I_BAD", "nope")

	if envInt("I_OK", 1) != 42 || envInt("I_BAD", 7) != 7 || envInt("I_MISSING", 9) != 9 {
		t.Error("envInt parsing/fallback wrong")
	}

	t.Setenv("F_OK", "1.5")
	t.Setenv("F_BAD", "nope")

	if envFloat("F_OK", 0) != 1.5 || envFloat("F_BAD", 3.5) != 3.5 || envFloat("F_MISSING", 2.5) != 2.5 {
		t.Error("envFloat parsing/fallback wrong")
	}

	t.Setenv("D_OK", "250ms")
	t.Setenv("D_BAD", "nope")

	if envDuration("D_OK", 0) != 250*time.Millisecond ||
		envDuration("D_BAD", time.Second) != time.Second ||
		envDuration("D_MISSING", time.Second) != time.Second {
		t.Error("envDuration parsing/fallback wrong")
	}
}

func TestEnvList(t *testing.T) {
	if got := envList("L_MISSING", []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Errorf("missing should return default, got %v", got)
	}

	t.Setenv("L_EMPTY", "")

	if got := envList("L_EMPTY", []string{"d"}); len(got) != 1 || got[0] != "d" {
		t.Errorf("empty should return default, got %v", got)
	}

	t.Setenv("L_VALS", "a, b ,,c")

	got := envList("L_VALS", nil)

	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("envList = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("envList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadOptionsRootsAndFallback(t *testing.T) {
	// ROOTS set → one volume per path, id/label from base name.
	t.Setenv("ROOTS", "/data/volume1,/data/volumeUSB1")
	t.Setenv("CACHE_DIR", "/tmp/c")

	opts := loadOptions(nil)
	if len(opts) != 2 {
		t.Fatalf("ROOTS: got %d volumes, want 2", len(opts))
	}

	if opts[0].ID != "volume1" || opts[0].DisplayRoot != "/volume1" ||
		opts[0].CachePath != "/tmp/c/volume1.json.gz" {
		t.Errorf("volume1 opts wrong: %+v", opts[0])
	}

	if opts[1].ID != "volumeUSB1" {
		t.Errorf("volume2 id = %q", opts[1].ID)
	}
}

func TestLoadOptionsSingleFallback(t *testing.T) {
	// No ROOTS → single-volume fallback.
	t.Setenv("ROOTS", "")
	t.Setenv("SCAN_ROOT", "/srv/data")
	t.Setenv("DISPLAY_ROOT", "/srv")

	opts := loadOptions(nil)
	if len(opts) != 1 {
		t.Fatalf("fallback: got %d, want 1", len(opts))
	}

	if opts[0].Root != "/srv/data" || opts[0].DisplayRoot != "/srv" {
		t.Errorf("fallback opts wrong: %+v", opts[0])
	}
}

func TestVolumeSetAdapter(t *testing.T) {
	mgr := scan.NewManager([]scan.Options{
		{ID: "a", Root: t.TempDir(), DisplayRoot: "/a"},
		{ID: "b", Root: t.TempDir(), DisplayRoot: "/b"},
	})
	vs := volumeSet{mgr}

	if len(vs.Volumes()) != 2 {
		t.Errorf("Volumes len = %d, want 2", len(vs.Volumes()))
	}

	if vs.Default() != "a" {
		t.Errorf("Default = %q, want a", vs.Default())
	}

	if _, ok := vs.Scanner("b"); !ok {
		t.Error("Scanner(b) should be found")
	}

	if _, ok := vs.Scanner("nope"); ok {
		t.Error("Scanner(nope) should not be found")
	}
}
