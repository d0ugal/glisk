// Command glisk scans a filesystem tree overnight and serves a DaisyDisk-style
// zoomable sunburst of where the space has gone.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/d0ugal/glisk/internal/scan"
	"github.com/d0ugal/glisk/internal/webui"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}

	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}

	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}

	return def
}

// envList splits a comma-separated env var, trimming blanks. An empty/unset
// value yields the default list.
func envList(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}

	var out []string

	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// volumeSet adapts *scan.Manager to webui.VolumeSet (whose Scanner returns the
// webui.Scanner interface rather than the concrete *scan.Scanner).
type volumeSet struct{ m *scan.Manager }

func (v volumeSet) Volumes() []scan.VolumeInfo { return v.m.Volumes() }
func (v volumeSet) Default() string            { return v.m.Default() }
func (v volumeSet) Scanner(id string) (webui.Scanner, bool) {
	s, ok := v.m.Scanner(id)
	if !ok {
		return nil, false
	}

	return s, true
}

// loadOptions builds the per-volume scan options from the environment. With
// ROOTS set it produces one volume per path (id/label from the base name);
// otherwise it falls back to a single volume from SCAN_ROOT/DISPLAY_ROOT.
func loadOptions(logger *slog.Logger) []scan.Options {
	base := scan.Options{
		ScanHour:     envInt("SCAN_HOUR", 2),
		TopK:         envInt("TOP_K", 24),
		MaxDepth:     envInt("MAX_DEPTH", 14),
		MinFraction:  envFloat("MIN_FRACTION", 0.0001),
		ThrottleN:    envInt("THROTTLE_EVERY", 4000),
		ThrottleDur:  envDuration("THROTTLE_SLEEP", 3*time.Millisecond),
		ExcludeGlobs: envList("EXCLUDE", []string{"@*"}),
		Logger:       logger,
	}
	cacheDir := env("CACHE_DIR", "/cache")

	var optsList []scan.Options

	if roots := envList("ROOTS", nil); len(roots) > 0 {
		for _, root := range roots {
			o := base
			o.Root = root
			o.ID = filepath.Base(root)
			o.DisplayRoot = "/" + o.ID
			o.CachePath = filepath.Join(cacheDir, o.ID+".json.gz")
			optsList = append(optsList, o)
		}
	} else {
		o := base
		o.Root = env("SCAN_ROOT", "/data")
		o.DisplayRoot = env("DISPLAY_ROOT", "")
		o.CachePath = env("CACHE_PATH", filepath.Join(cacheDir, "tree.json.gz"))
		optsList = append(optsList, o)
	}

	return optsList
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	optsList := loadOptions(logger)
	addr := env("HTTP_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mgr := scan.NewManager(optsList)
	mgr.Start(ctx)

	srv := &http.Server{
		Addr:              addr,
		Handler:           webui.New(volumeSet{mgr}, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("glisk listening", "addr", addr, "volumes", len(optsList))

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = srv.Shutdown(shutdownCtx)
}
