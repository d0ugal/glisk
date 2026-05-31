package scan

import (
	"context"
	"sync"
)

// VolumeInfo is the per-volume summary used to populate the UI's path switcher.
type VolumeInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status Status `json:"status"`
}

// Manager owns one Scanner per configured volume and shares a single scan gate
// between them so their nightly walks run one at a time.
type Manager struct {
	order []*Scanner
	byID  map[string]*Scanner
}

// NewManager builds a Manager for the given option sets, wiring a shared scan
// gate into each so only one volume walks at a time.
func NewManager(opts []Options) *Manager {
	gate := &sync.Mutex{}
	m := &Manager{byID: make(map[string]*Scanner, len(opts))}
	for _, o := range opts {
		o.ScanGate = gate
		s := New(o)
		m.order = append(m.order, s)
		m.byID[s.ID()] = s
	}
	return m
}

// Start launches every volume's scanner.
func (m *Manager) Start(ctx context.Context) {
	for _, s := range m.order {
		s.Start(ctx)
	}
}

// Volumes returns the per-volume summaries in configured order.
func (m *Manager) Volumes() []VolumeInfo {
	out := make([]VolumeInfo, 0, len(m.order))
	for _, s := range m.order {
		out = append(out, VolumeInfo{ID: s.ID(), Label: s.Label(), Status: s.Status()})
	}
	return out
}

// Scanner returns the scanner for an id.
func (m *Manager) Scanner(id string) (*Scanner, bool) {
	s, ok := m.byID[id]
	return s, ok
}

// Default returns the id of the first configured volume ("" if none).
func (m *Manager) Default() string {
	if len(m.order) == 0 {
		return ""
	}
	return m.order[0].ID()
}
