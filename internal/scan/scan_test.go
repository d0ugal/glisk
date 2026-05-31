package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file of the given size (in bytes) under dir.
func writeFile(t *testing.T, path string, size int) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

// buildTree is a small helper that walks a directory and returns the projected
// tree plus totals, mirroring what runScan does without the scheduler.
func buildTree(t *testing.T, root string, opts Options) (*Node, int64, int64, int64) {
	t.Helper()

	opts.Root = root
	s := New(opts)

	raw, files, dirs, err := s.buildRaw(context.Background())
	if err != nil {
		t.Fatalf("buildRaw: %v", err)
	}

	total := computeSize(raw)
	tree := s.project(raw, 0, int64(s.opts.MinFraction*float64(total)))

	return tree, total, files, dirs
}

func TestScanComputesSizesAndCounts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), 1000)
	writeFile(t, filepath.Join(root, "sub", "b.bin"), 2000)
	writeFile(t, filepath.Join(root, "sub", "deep", "c.bin"), 500)

	tree, total, files, dirs := buildTree(t, root, Options{})

	if files != 3 {
		t.Errorf("files = %d, want 3", files)
	}

	if dirs != 2 { // sub, sub/deep
		t.Errorf("dirs = %d, want 2", dirs)
	}

	if total != 3500 {
		t.Errorf("total = %d, want 3500", total)
	}

	if tree.Size != 3500 {
		t.Errorf("tree root size = %d, want 3500", tree.Size)
	}

	// The "sub" directory should aggregate to 2500 (2000 + 500).
	var sub *Node

	for _, c := range tree.Children {
		if c.Name == "sub" {
			sub = c
		}
	}

	if sub == nil {
		t.Fatal("expected a 'sub' child")
	}

	if sub.Size != 2500 {
		t.Errorf("sub size = %d, want 2500", sub.Size)
	}

	if !sub.Dir {
		t.Error("sub should be marked as a directory")
	}
}

func TestProjectFoldsSmallChildrenAndCapsTopK(t *testing.T) {
	root := t.TempDir()
	// Three large files plus many tiny ones; TopK=3 keeps the big ones and
	// folds the rest into a single "N more" bucket.
	writeFile(t, filepath.Join(root, "big1"), 10000)
	writeFile(t, filepath.Join(root, "big2"), 9000)
	writeFile(t, filepath.Join(root, "big3"), 8000)

	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(root, "tiny", "t"+string(rune('a'+i))), 1)
	}

	tree, total, _, _ := buildTree(t, root, Options{TopK: 3, MinFraction: 0})

	// Root keeps at most 3 real children + 1 aggregate bucket.
	if len(tree.Children) != 4 {
		t.Fatalf("root children = %d, want 4 (3 + bucket)", len(tree.Children))
	}

	var bucket *Node

	for _, c := range tree.Children {
		if c.Children == nil && c.Name != "big1" && c.Name != "big2" && c.Name != "big3" {
			bucket = c
		}
	}

	if bucket == nil {
		t.Fatal("expected an aggregate bucket child")
	}

	// Sizes must still sum to the true total — nothing lost when folding.
	var sum int64
	for _, c := range tree.Children {
		sum += c.Size
	}

	if sum != total {
		t.Errorf("children sum = %d, want %d", sum, total)
	}
}

func TestScanExcludesGlobs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "nas", "real.bin"), 5000)
	writeFile(t, filepath.Join(root, "@docker", "btrfs", "huge.bin"), 999999)
	writeFile(t, filepath.Join(root, "@appdata", "x.bin"), 12345)

	tree, total, files, _ := buildTree(t, root, Options{ExcludeGlobs: []string{"@*"}})

	// Only the real share is counted; the @-dirs are skipped entirely.
	if files != 1 {
		t.Errorf("files = %d, want 1 (only nas/real.bin)", files)
	}

	if total != 5000 {
		t.Errorf("total = %d, want 5000 (excluded dirs must not contribute)", total)
	}

	for _, c := range tree.Children {
		if c.Name == "@docker" || c.Name == "@appdata" {
			t.Errorf("excluded dir %q should not appear in the tree", c.Name)
		}
	}
}

func TestProjectRespectsMaxDepth(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "l1", "l2", "l3", "f.bin"), 100)

	// MaxDepth=1 means only the first level below root is kept; deeper nodes
	// are collapsed but their size still rolls up.
	tree, total, _, _ := buildTree(t, root, Options{MaxDepth: 1})

	if tree.Size != total || total != 100 {
		t.Fatalf("total/tree size = %d/%d, want 100/100", total, tree.Size)
	}

	if len(tree.Children) != 1 || tree.Children[0].Name != "l1" {
		t.Fatalf("expected single child 'l1', got %+v", tree.Children)
	}

	if tree.Children[0].Children != nil {
		t.Errorf("l1 should have no children at MaxDepth=1, got %d", len(tree.Children[0].Children))
	}

	if tree.Children[0].Size != 100 {
		t.Errorf("l1 size = %d, want 100", tree.Children[0].Size)
	}
}

func TestRescanSubtreeUpdatesTreeAndTotal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "nas", "a.bin"), 1000)
	writeFile(t, filepath.Join(root, "other", "b.bin"), 500)

	s := New(Options{Root: root, DisplayRoot: "/volume1"})
	// Seed the cache via a full scan (synchronously).
	s.runScan(context.Background())

	before := s.Result()
	if before.Status.TotalBytes != 1500 {
		t.Fatalf("seed total = %d, want 1500", before.Status.TotalBytes)
	}

	// Grow the nas folder on disk, then rescan just that folder.
	writeFile(t, filepath.Join(root, "nas", "c.bin"), 4000)

	if err := s.RescanSubtree(context.Background(), []string{"nas"}); err != nil {
		t.Fatalf("RescanSubtree: %v", err)
	}

	after := s.Result()
	if after.Status.TotalBytes != 5500 { // 5000 (nas) + 500 (other)
		t.Errorf("total after rescan = %d, want 5500", after.Status.TotalBytes)
	}

	_, nas := childByName(after.Tree, "nas")
	if nas == nil || nas.Size != 5000 {
		t.Errorf("nas size after rescan = %v, want 5000", nas)
	}

	if after.Tree.Size != 5500 {
		t.Errorf("root size = %d, want 5500", after.Tree.Size)
	}
}

func TestRescanSubtreeRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "x.bin"), 10)
	s := New(Options{Root: root, DisplayRoot: "/volume1"})
	s.runScan(context.Background())

	if err := s.RescanSubtree(context.Background(), []string{".."}); err == nil {
		t.Error("expected error for .. segment")
	}

	if err := s.RescanSubtree(context.Background(), []string{"nope"}); err == nil {
		t.Error("expected error for missing folder")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "tree.json.gz")
	s := New(Options{Root: dir, CachePath: cache})

	want := &Result{
		Status: Status{HasData: true, TotalBytes: 4242, Files: 7},
		Tree:   &Node{Name: "/volume1", Size: 4242, Dir: true},
	}
	if err := s.saveCache(want); err != nil {
		t.Fatalf("saveCache: %v", err)
	}

	got, err := s.loadCache()
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}

	if got == nil || got.Tree == nil {
		t.Fatal("loadCache returned nil result/tree")
	}

	if got.Status.TotalBytes != 4242 || got.Tree.Size != 4242 || got.Tree.Name != "/volume1" {
		t.Errorf("round-trip mismatch: %+v / %+v", got.Status, got.Tree)
	}
}

func TestLoadCacheMissingFile(t *testing.T) {
	s := New(Options{CachePath: filepath.Join(t.TempDir(), "does-not-exist.gz")})

	got, err := s.loadCache()
	if err != nil {
		t.Fatalf("expected no error for missing cache, got %v", err)
	}

	if got != nil {
		t.Errorf("expected nil result for missing cache, got %+v", got)
	}
}
