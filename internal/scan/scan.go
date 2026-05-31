// Package scan walks a filesystem tree and produces a bounded, sunburst-ready
// size tree. Scanning is deliberately gentle on the host: it runs on a single
// OS thread pinned to a low scheduling priority, throttles itself periodically,
// and only runs overnight (or on explicit demand) rather than continuously.
package scan

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Node is a single entry in the projected size tree sent to the frontend.
// Directories carry Children; files (and aggregated "N more" buckets) do not.
type Node struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Dir      bool    `json:"dir,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// Status is the scanner's current state, safe to serialise to the UI.
type Status struct {
	ID           string  `json:"id"`
	Scanning     bool    `json:"scanning"`
	HasData      bool    `json:"hasData"`
	Files        int64   `json:"files"`
	Dirs         int64   `json:"dirs"`
	TotalBytes   int64   `json:"totalBytes"`
	LastScanUnix int64   `json:"lastScanUnix"`
	DurationSec  float64 `json:"durationSec"`
	NextScanUnix int64   `json:"nextScanUnix"`
	Progress     int64   `json:"progress"`
	Root         string  `json:"root"`
	Error        string  `json:"error,omitempty"`
}

// Result bundles the status and tree for caching and the /api/tree response.
type Result struct {
	Status Status `json:"status"`
	Tree   *Node  `json:"tree"`
}

// Options configures a Scanner.
type Options struct {
	ID          string // stable identifier for this volume (e.g. "volume1")
	Root        string // path actually walked (e.g. /data)
	DisplayRoot string // label shown at the centre (e.g. /volume1)
	CachePath   string // gzipped-JSON cache file; "" disables persistence
	// ScanGate, when shared across scanners, serialises their walks so two
	// nightly scans don't run at once and double the peak memory.
	ScanGate    *sync.Mutex
	ScanHour    int           // local hour-of-day for the nightly scan (0-23)
	TopK        int           // max children kept per directory in the projection
	MaxDepth    int           // deepest level kept in the projection
	MinFraction float64       // children smaller than this fraction of total are folded away
	ThrottleN   int           // sleep after every N entries walked (0 disables)
	ThrottleDur time.Duration // how long to sleep when throttling
	// ExcludeGlobs skips any directory whose base name matches one of these
	// shell patterns (filepath.Match). On Synology, "@*" drops the @docker,
	// @appdata, @eaDir, … subvolumes whose reflinked/snapshot contents would
	// otherwise massively over-report disk usage.
	ExcludeGlobs []string
	Logger       *slog.Logger
}

// Scanner owns the cached scan result and runs scans serially.
type Scanner struct {
	opts Options
	log  *slog.Logger

	mu     sync.RWMutex
	result *Result // last completed scan (or loaded cache)

	scanning     atomic.Bool
	progress     atomic.Int64
	nextScanUnix atomic.Int64

	trigger chan struct{}
}

// New constructs a Scanner, applying sensible defaults to zero-valued options.
func New(opts Options) *Scanner {
	if opts.DisplayRoot == "" {
		opts.DisplayRoot = opts.Root
	}

	if opts.ID == "" {
		opts.ID = filepath.Base(opts.DisplayRoot)
	}

	if opts.TopK <= 0 {
		opts.TopK = 24
	}

	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 14
	}

	if opts.MinFraction <= 0 {
		opts.MinFraction = 0.0001 // 0.01%
	}

	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Scanner{
		opts:    opts,
		log:     opts.Logger,
		trigger: make(chan struct{}, 1),
	}
}

// ID returns the volume's stable identifier.
func (s *Scanner) ID() string { return s.opts.ID }

// Label returns the human-facing root label (e.g. "/volume1").
func (s *Scanner) Label() string { return s.opts.DisplayRoot }

// Start loads any cached result and launches the scan worker and the nightly
// scheduler. It never kicks off a scan on startup — by design the first scan
// happens at the scheduled hour or when the user requests one.
func (s *Scanner) Start(ctx context.Context) {
	if r, err := s.loadCache(); err != nil {
		s.log.Warn("could not load cache", "err", err)
	} else if r != nil {
		r.Status.Scanning = false

		s.mu.Lock()
		s.result = r
		s.mu.Unlock()
		s.log.Info("loaded cached scan", "files", r.Status.Files, "bytes", r.Status.TotalBytes)
	}

	go s.worker(ctx)
	go s.scheduler(ctx)
}

// Trigger requests a scan. It is non-blocking: if a scan is already queued or
// running the request is coalesced. Returns false if a scan is in progress.
func (s *Scanner) Trigger() bool {
	if s.scanning.Load() {
		return false
	}

	select {
	case s.trigger <- struct{}{}:
		return true
	default:
		return true // already queued
	}
}

// Status returns a snapshot of the current state.
func (s *Scanner) Status() Status {
	s.mu.RLock()

	var st Status
	if s.result != nil {
		st = s.result.Status
	}

	s.mu.RUnlock()

	st.ID = s.opts.ID
	st.Root = s.opts.DisplayRoot

	st.NextScanUnix = s.nextScanUnix.Load()
	if s.scanning.Load() {
		st.Scanning = true
		st.Progress = s.progress.Load()
	}

	return st
}

// Result returns the last completed scan (status + tree). Tree may be nil.
func (s *Scanner) Result() Result {
	s.mu.RLock()

	var tree *Node
	if s.result != nil {
		tree = s.result.Tree
	}

	s.mu.RUnlock()

	return Result{Status: s.Status(), Tree: tree}
}

// worker runs scans serially on a dedicated, low-priority OS thread.
func (s *Scanner) worker(ctx context.Context) {
	// Pin to one OS thread and drop its scheduling priority so the scan only
	// uses spare CPU. setpriority on Linux acts per-thread for the caller.
	runtime.LockOSThread()

	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19); err != nil {
		s.log.Debug("could not lower scan priority", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			s.runScan(ctx)
		}
	}
}

// scheduler fires a trigger once per day at the configured hour.
func (s *Scanner) scheduler(ctx context.Context) {
	for {
		now := time.Now()

		next := time.Date(now.Year(), now.Month(), now.Day(), s.opts.ScanHour, 0, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}

		s.nextScanUnix.Store(next.Unix())
		s.log.Info("next scheduled scan", "at", next.Format(time.RFC3339))

		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.Trigger()
		}
	}
}

func (s *Scanner) runScan(ctx context.Context) {
	s.scanning.Store(true)
	s.progress.Store(0)

	start := time.Now()

	// Serialise the heavy walk against other volumes' scans, if a gate is set.
	if s.opts.ScanGate != nil {
		s.opts.ScanGate.Lock()
	}

	s.log.Info("scan started", "root", s.opts.Root)
	root, files, dirs, walkErr := s.buildRaw(ctx)

	var (
		total int64
		tree  *Node
	)

	if root != nil {
		total = computeSize(root)
		tree = s.project(root, 0, int64(s.opts.MinFraction*float64(total)))
	}
	// Hint the GC to reclaim the transient raw tree before the cache write so
	// steady-state memory stays small (only the projected tree is retained).
	runtime.GC()

	if s.opts.ScanGate != nil {
		s.opts.ScanGate.Unlock()
	}

	if ctx.Err() != nil {
		s.scanning.Store(false)
		return
	}

	st := Status{
		Scanning:     false,
		HasData:      true,
		Files:        files,
		Dirs:         dirs,
		TotalBytes:   total,
		LastScanUnix: time.Now().Unix(),
		DurationSec:  time.Since(start).Seconds(),
		Root:         s.opts.DisplayRoot,
	}
	if walkErr != nil {
		st.Error = walkErr.Error()
	}

	res := &Result{Status: st, Tree: tree}

	s.mu.Lock()
	s.result = res
	s.mu.Unlock()
	s.scanning.Store(false)

	if err := s.saveCache(res); err != nil {
		s.log.Warn("could not save cache", "err", err)
	}

	s.log.Info("scan finished",
		"files", files, "dirs", dirs, "bytes", total,
		"duration", time.Since(start).Round(time.Millisecond))
}

// rawNode is the full in-memory tree built during a walk. It is discarded once
// the bounded projection has been produced.
type rawNode struct {
	name     string
	size     int64
	isDir    bool
	children map[string]*rawNode
}

func (s *Scanner) buildRaw(ctx context.Context) (root *rawNode, files, dirs int64, walkErr error) {
	return s.buildRawAt(ctx, s.opts.Root, s.opts.DisplayRoot, true)
}

// buildRawAt walks fsRoot into a raw tree labelled rootName. When throttle is
// true the walk paces itself (used for the gentle nightly scan); on-demand
// folder rescans pass false for snappy feedback.
func (s *Scanner) buildRawAt(ctx context.Context, fsRoot, rootName string, throttle bool) (root *rawNode, files, dirs int64, walkErr error) {
	root = &rawNode{name: rootName, isDir: true, children: map[string]*rawNode{}}

	var count int64

	walkErr = filepath.WalkDir(fsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep going
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if d.IsDir() && s.excluded(d.Name()) {
			return filepath.SkipDir
		}

		count++
		if throttle && s.opts.ThrottleN > 0 && s.opts.ThrottleDur > 0 && count%int64(s.opts.ThrottleN) == 0 {
			s.progress.Store(count)
			time.Sleep(s.opts.ThrottleDur)
		}

		rel, e := filepath.Rel(fsRoot, path)
		if e != nil || rel == "." {
			return nil
		}

		parts := strings.Split(rel, string(os.PathSeparator))

		cur := root
		for i, p := range parts {
			child := cur.children[p]
			if child == nil {
				child = &rawNode{name: p, children: map[string]*rawNode{}}
				cur.children[p] = child
			}

			last := i == len(parts)-1
			if last {
				if d.IsDir() {
					child.isDir = true
					dirs++
				} else {
					if info, ie := d.Info(); ie == nil {
						child.size = info.Size()
					}

					files++
				}
			} else {
				child.isDir = true
			}

			cur = child
		}

		return nil
	})

	s.progress.Store(count)

	return root, files, dirs, walkErr
}

// RescanSubtree re-walks a single folder (identified by path segments below
// the display root) and splices the fresh result into the cached tree,
// updating ancestor sizes and the total. It is meant for quick "I just changed
// this folder" feedback without waiting for a full nightly-style scan, so the
// walk is not throttled. Returns an error if a full scan is running, the path
// escapes the root, or the folder is missing from the current tree.
func (s *Scanner) RescanSubtree(ctx context.Context, segments []string) error {
	if s.scanning.Load() {
		return errors.New("a full scan is in progress")
	}

	if len(segments) == 0 {
		return errors.New("no folder specified")
	}

	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsRune(seg, os.PathSeparator) {
			return fmt.Errorf("invalid path segment %q", seg)
		}
	}

	fsPath := filepath.Join(append([]string{s.opts.Root}, segments...)...)
	// Defend against traversal: the cleaned path must stay under Root.
	rel, err := filepath.Rel(s.opts.Root, fsPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("path escapes root")
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", fsPath)
	}

	name := segments[len(segments)-1]

	raw, files, dirs, walkErr := s.buildRawAt(ctx, fsPath, name, false)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if walkErr != nil {
		return walkErr
	}

	computeSize(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.result == nil || s.result.Tree == nil {
		return errors.New("no existing scan to update; run a full scan first")
	}

	tree := s.result.Tree
	minSize := int64(s.opts.MinFraction * float64(s.result.Status.TotalBytes))
	node := s.project(raw, len(segments), minSize)

	parent, chain, idx, ok := locateParent(tree, segments)
	if !ok {
		return errors.New("folder's parent is not in the current tree")
	}

	var oldSize int64
	if idx >= 0 {
		oldSize = parent.Children[idx].Size
		parent.Children[idx] = node
	} else {
		parent.Children = append(parent.Children, node)
	}

	delta := node.Size - oldSize
	for _, n := range chain {
		n.Size += delta
	}

	s.result.Status.TotalBytes = tree.Size
	s.result.Status.LastScanUnix = time.Now().Unix()

	if err := s.saveCache(s.result); err != nil {
		s.log.Warn("could not save cache after folder rescan", "err", err)
	}

	s.log.Info("folder rescan",
		"path", strings.Join(segments, "/"), "files", files, "dirs", dirs, "bytes", node.Size)

	return nil
}

// childByName finds a child node by exact name.
func childByName(n *Node, name string) (int, *Node) {
	for i, c := range n.Children {
		if c.Name == name {
			return i, c
		}
	}

	return -1, nil
}

// locateParent walks segments (below the root) and returns the parent of the
// final segment, the ancestor chain root..parent, and the index of the target
// child within the parent (-1 if not yet present). parentOK is false when an
// intermediate directory is absent from the projected tree.
func locateParent(root *Node, segments []string) (parent *Node, chain []*Node, childIdx int, parentOK bool) {
	cur := root
	chain = []*Node{root}

	for i := 0; i < len(segments)-1; i++ {
		_, next := childByName(cur, segments[i])
		if next == nil {
			return nil, nil, -1, false
		}

		cur = next
		chain = append(chain, cur)
	}

	idx, _ := childByName(cur, segments[len(segments)-1])

	return cur, chain, idx, true
}

// excluded reports whether a directory base name matches any exclude glob.
func (s *Scanner) excluded(base string) bool {
	for _, g := range s.opts.ExcludeGlobs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}

	return false
}

// computeSize fills in directory sizes bottom-up and returns the total.
func computeSize(n *rawNode) int64 {
	if !n.isDir {
		return n.size
	}

	var total int64
	for _, c := range n.children {
		total += computeSize(c)
	}

	n.size = total

	return total
}

// project turns the raw tree into a bounded Node tree: at most TopK children
// per directory, no deeper than MaxDepth, with anything smaller than minSize
// folded into a single "N more" bucket.
func (s *Scanner) project(n *rawNode, depth int, minSize int64) *Node {
	node := &Node{Name: n.name, Size: n.size, Dir: n.isDir}
	if !n.isDir || len(n.children) == 0 || depth >= s.opts.MaxDepth {
		return node
	}

	kids := make([]*rawNode, 0, len(n.children))
	for _, c := range n.children {
		kids = append(kids, c)
	}

	sort.Slice(kids, func(i, j int) bool { return kids[i].size > kids[j].size })

	var (
		restSize  int64
		restCount int
	)

	for i, c := range kids {
		if i < s.opts.TopK && c.size >= minSize {
			node.Children = append(node.Children, s.project(c, depth+1, minSize))
		} else {
			restSize += c.size
			restCount++
		}
	}

	if restCount > 0 {
		label := fmt.Sprintf("%d more items", restCount)
		if restCount == 1 {
			label = "1 more item"
		}

		node.Children = append(node.Children, &Node{Name: label, Size: restSize})
	}

	return node
}

func (s *Scanner) loadCache() (*Result, error) {
	if s.opts.CachePath == "" {
		return nil, nil
	}

	f, err := os.Open(s.opts.CachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	var r Result
	if err := json.NewDecoder(gz).Decode(&r); err != nil {
		return nil, err
	}

	return &r, nil
}

func (s *Scanner) saveCache(r *Result) error {
	if s.opts.CachePath == "" {
		return nil
	}

	tmp := s.opts.CachePath + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(f)
	if err := json.NewEncoder(gz).Encode(r); err != nil {
		_ = gz.Close()
		_ = f.Close()

		return err
	}

	if err := gz.Close(); err != nil {
		_ = f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmp, s.opts.CachePath)
}
