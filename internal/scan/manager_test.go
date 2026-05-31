package scan

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManagerScansEachVolume(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeFile(t, filepath.Join(rootA, "a.bin"), 1000)
	writeFile(t, filepath.Join(rootB, "b.bin"), 2000)

	m := NewManager([]Options{
		{ID: "volA", Root: rootA, DisplayRoot: "/volA"},
		{ID: "volB", Root: rootB, DisplayRoot: "/volB"},
	})

	// Scan both directly (bypassing the scheduler).
	for _, id := range []string{"volA", "volB"} {
		s, ok := m.Scanner(id)
		if !ok {
			t.Fatalf("missing scanner %q", id)
		}
		s.runScan(context.Background())
	}

	vols := m.Volumes()
	if len(vols) != 2 {
		t.Fatalf("Volumes len = %d, want 2", len(vols))
	}
	if vols[0].ID != "volA" || vols[1].ID != "volB" {
		t.Errorf("order/ids wrong: %+v", vols)
	}
	if vols[0].Status.TotalBytes != 1000 || vols[1].Status.TotalBytes != 2000 {
		t.Errorf("sizes wrong: %d / %d", vols[0].Status.TotalBytes, vols[1].Status.TotalBytes)
	}
	if vols[0].Status.ID != "volA" {
		t.Errorf("status ID = %q, want volA", vols[0].Status.ID)
	}

	if def := m.Default(); def != "volA" {
		t.Errorf("Default = %q, want volA", def)
	}
	if _, ok := m.Scanner("nope"); ok {
		t.Error("Scanner(nope) should be false")
	}
}

func TestManagerSharesScanGate(t *testing.T) {
	m := NewManager([]Options{
		{ID: "a", Root: t.TempDir()},
		{ID: "b", Root: t.TempDir()},
	})
	sa, _ := m.Scanner("a")
	sb, _ := m.Scanner("b")
	if sa.opts.ScanGate == nil || sa.opts.ScanGate != sb.opts.ScanGate {
		t.Error("expected both scanners to share one ScanGate")
	}
}
