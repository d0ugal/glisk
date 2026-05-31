package scan

import (
	"context"
	"testing"
)

func TestManagerStartStops(t *testing.T) {
	m := NewManager([]Options{{ID: "a", Root: t.TempDir(), DisplayRoot: "/a"}})
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx) // launches worker + scheduler goroutines
	cancel()     // they should observe ctx.Done and return
	if len(m.Volumes()) != 1 {
		t.Errorf("Volumes len = %d, want 1", len(m.Volumes()))
	}
}

func TestManagerEmptyDefault(t *testing.T) {
	m := NewManager(nil)
	if m.Default() != "" {
		t.Errorf("empty manager Default = %q, want \"\"", m.Default())
	}
	if len(m.Volumes()) != 0 {
		t.Error("empty manager should have no volumes")
	}
}
