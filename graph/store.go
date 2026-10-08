package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileStore keeps one JSON file per thread under Dir. You can cat a run.
type FileStore[S any] struct {
	Dir string
}

func (f FileStore[S]) path(thread string) string {
	return filepath.Join(f.Dir, thread, "checkpoint.json")
}

// Load implements Store.
func (f FileStore[S]) Load(_ context.Context, thread string) (*Checkpoint[S], error) {
	data, err := os.ReadFile(f.path(thread))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, thread)
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint[S]
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("graph: checkpoint %s: %w", thread, err)
	}
	return &cp, nil
}

// Save implements Store. The write is atomic: a crash mid-save leaves the previous file.
func (f FileStore[S]) Save(_ context.Context, cp *Checkpoint[S]) error {
	path := f.path(cp.Thread)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MemoryStore is a Store for tests. It round-trips through JSON so tests exercise the
// same serialization as FileStore.
type MemoryStore[S any] struct {
	mu   sync.Mutex
	data map[string][]byte
}

// Load implements Store.
func (m *MemoryStore[S]) Load(_ context.Context, thread string) (*Checkpoint[S], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[thread]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, thread)
	}
	var cp Checkpoint[S]
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// Save implements Store.
func (m *MemoryStore[S]) Save(_ context.Context, cp *Checkpoint[S]) error {
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[string][]byte)
	}
	m.data[cp.Thread] = raw
	return nil
}
