// Package metrics counts what the models cost: calls, tokens and wall time, per model.
// The ledger says who decided each turn; the meter says what each decider cost.
package metrics

import (
	"slices"
	"sync"
	"time"
)

// Row is the usage of one model.
type Row struct {
	Model        string        `json:"model"`
	Calls        int           `json:"calls"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	Duration     time.Duration `json:"duration"`
	Errors       int           `json:"errors"`
}

// Meter accumulates rows. It is safe for concurrent use and a nil *Meter is a no-op, so
// adapters can take one optionally.
type Meter struct {
	mu   sync.Mutex
	rows map[string]*Row
}

// New returns an empty meter.
func New() *Meter { return &Meter{rows: make(map[string]*Row)} }

// Record adds one call. Token counts may be zero when a provider does not report them.
func (m *Meter) Record(model string, in, out int, d time.Duration, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[model]
	if !ok {
		r = &Row{Model: model}
		m.rows[model] = r
	}
	r.Calls++
	r.InputTokens += in
	r.OutputTokens += out
	r.Duration += d
	if failed {
		r.Errors++
	}
}

// Rows returns a copy of the usage, sorted by model name.
func (m *Meter) Rows() []Row {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Row, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b Row) int {
		switch {
		case a.Model < b.Model:
			return -1
		case a.Model > b.Model:
			return 1
		}
		return 0
	})
	return out
}
