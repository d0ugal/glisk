package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStartScanAndCacheReload exercises Start → worker → runScan → saveCache,
// then a fresh scanner loading that cache on Start.
func TestStartScanAndCacheReload(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "f.bin"), 500)
	cache := filepath.Join(t.TempDir(), "v.json.gz")

	ctx, cancel := context.WithCancel(context.Background())
	s := New(Options{Root: root, DisplayRoot: "/v", CachePath: cache})
	s.Start(ctx)

	if !s.Trigger() {
		t.Fatal("Trigger returned false")
	}

	deadline := time.Now().Add(5 * time.Second)

	for {
		st := s.Status()
		if st.HasData && !st.Scanning {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("scan did not finish in time")
		}

		time.Sleep(20 * time.Millisecond)
	}

	cancel()

	if r := s.Result(); r.Tree == nil || r.Status.TotalBytes != 500 {
		t.Fatalf("unexpected result after scan: %+v", r.Status)
	}

	// A new scanner over the same cache should load it on Start.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	s2 := New(Options{Root: root, DisplayRoot: "/v", CachePath: cache})
	s2.Start(ctx2)

	if st := s2.Status(); !st.HasData || st.TotalBytes != 500 {
		t.Errorf("cache not reloaded: %+v", st)
	}
}

func TestTriggerCoalescesWhileScanning(t *testing.T) {
	s := New(Options{Root: t.TempDir()})
	s.scanning.Store(true)

	if s.Trigger() {
		t.Error("Trigger should return false while a scan is running")
	}
}

func TestRescanSubtreeErrorPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep", "a.bin"), 100)
	writeFile(t, filepath.Join(root, "afile"), 10)
	s := New(Options{Root: root, DisplayRoot: "/v"})
	s.runScan(context.Background())

	cases := []struct {
		name string
		segs []string
	}{
		{"empty", nil},
		{"dotdot", []string{".."}},
		{"separator", []string{"a/b"}},
		{"missing", []string{"nope"}},
		{"not-a-dir", []string{"afile"}},
		{"parent-missing", []string{"ghost", "child"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.RescanSubtree(context.Background(), c.segs); err == nil {
				t.Errorf("expected error for %v", c.segs)
			}
		})
	}

	// Full scan in progress → rejected.
	s.scanning.Store(true)

	if err := s.RescanSubtree(context.Background(), []string{"keep"}); err == nil {
		t.Error("expected error while a full scan is in progress")
	}

	s.scanning.Store(false)
}

func TestRescanSubtreeInsertsNewFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep", "a.bin"), 100)
	s := New(Options{Root: root, DisplayRoot: "/v"})
	s.runScan(context.Background())

	// A folder created after the initial scan is absent from the tree; a
	// targeted rescan should insert it and grow the total.
	writeFile(t, filepath.Join(root, "fresh", "big.bin"), 9000)

	if err := s.RescanSubtree(context.Background(), []string{"fresh"}); err != nil {
		t.Fatalf("RescanSubtree: %v", err)
	}

	r := s.Result()
	if _, n := childByName(r.Tree, "fresh"); n == nil || n.Size != 9000 {
		t.Errorf("fresh folder not inserted correctly: %+v", n)
	}

	if r.Tree.Size != 9100 {
		t.Errorf("root total = %d, want 9100", r.Tree.Size)
	}
}

func TestRescanSubtreeNoPriorScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "d", "a.bin"), 10)

	s := New(Options{Root: root})
	if err := s.RescanSubtree(context.Background(), []string{"d"}); err == nil {
		t.Error("expected error when no prior scan exists")
	}
}

func TestLoadCacheCorrupt(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.json.gz")
	if err := os.WriteFile(f, []byte("definitely not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(Options{CachePath: f})
	if _, err := s.loadCache(); err == nil {
		t.Error("expected error decoding corrupt cache")
	}
}

func TestProjectSingularMoreBucket(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "big"), 10000)
	writeFile(t, filepath.Join(root, "small"), 5)

	tree, total, _, _ := buildTree(t, root, Options{TopK: 1, MinFraction: 0})
	if total != 10005 {
		t.Fatalf("total = %d, want 10005", total)
	}

	var bucket *Node

	for _, c := range tree.Children {
		if c.Name == "1 more item" {
			bucket = c
		}
	}

	if bucket == nil {
		t.Fatalf("expected a '1 more item' bucket, got %+v", tree.Children)
	}
}

func TestThrottlePathRuns(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(root, "f", string(rune('a'+i))), 1)
	}
	// ThrottleN=1 with a tiny sleep exercises the throttle branch.
	tree, total, _, _ := buildTree(t, root, Options{ThrottleN: 1, ThrottleDur: time.Microsecond})
	if total != 5 || tree.Size != 5 {
		t.Errorf("total=%d treeSize=%d, want 5/5", total, tree.Size)
	}
}

func TestLocateParent(t *testing.T) {
	root := &Node{Name: "/", Children: []*Node{
		{Name: "a", Children: []*Node{{Name: "b"}}},
	}}

	p, chain, idx, ok := locateParent(root, []string{"a", "b"})
	if !ok || idx < 0 || p.Name != "a" || len(chain) != 2 {
		t.Errorf("found child: ok=%v idx=%d parent=%s chain=%d", ok, idx, p.Name, len(chain))
	}

	if _, _, idx, ok := locateParent(root, []string{"a", "missing"}); !ok || idx != -1 {
		t.Errorf("missing child: want ok=true idx=-1, got ok=%v idx=%d", ok, idx)
	}

	if _, _, _, ok := locateParent(root, []string{"ghost", "x"}); ok {
		t.Error("missing intermediate: want ok=false")
	}

	if p, _, _, ok := locateParent(root, []string{"a"}); !ok || p != root {
		t.Error("single segment: parent should be root")
	}
}

func TestSaveCacheError(t *testing.T) {
	s := New(Options{CachePath: "/nonexistent-xyz-dir/sub/cache.json.gz"})
	if err := s.saveCache(&Result{Tree: &Node{Name: "x"}}); err == nil {
		t.Error("expected error creating cache in a missing directory")
	}
}

func TestExcludeNeverSkipsTheRoot(t *testing.T) {
	parent := t.TempDir()
	// A scan root whose own base name matches an exclude glob.
	scanRoot := filepath.Join(parent, "@scanme")
	writeFile(t, filepath.Join(scanRoot, "a.bin"), 1000)
	// A descendant that should still be excluded by the same glob.
	writeFile(t, filepath.Join(scanRoot, "@cache", "big.bin"), 9999)

	s := New(Options{ExcludeGlobs: []string{"@*"}})

	raw, files, _, err := s.buildRawAt(context.Background(), scanRoot, "@scanme", false)
	if err != nil {
		t.Fatalf("buildRawAt: %v", err)
	}

	total := computeSize(raw)

	// Root must NOT be skipped: a.bin is counted.
	if files != 1 || total != 1000 {
		t.Errorf("root scan: files=%d total=%d, want 1/1000 (root must not be excluded)", files, total)
	}
	// The descendant @cache must still be excluded.
	for _, c := range raw.children {
		if c.name == "@cache" {
			t.Error("descendant @cache should have been excluded")
		}
	}
}

func TestExcludedMatchesAnyGlob(t *testing.T) {
	s := New(Options{ExcludeGlobs: []string{"@*", "#recycle", "node_modules"}})
	for _, in := range []string{"@docker", "#recycle", "node_modules"} {
		if !s.excluded(in) {
			t.Errorf("%q should be excluded", in)
		}
	}

	for _, in := range []string{"nas", "Media", "frigate"} {
		if s.excluded(in) {
			t.Errorf("%q should not be excluded", in)
		}
	}
}
