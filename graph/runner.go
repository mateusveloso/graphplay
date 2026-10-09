package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Step is one executed node, kept in the checkpoint so a run explains itself.
type Step struct {
	Node string        `json:"node"`
	At   time.Time     `json:"at"`
	Took time.Duration `json:"took"`
}

// Pause records where a run stopped and what it is asking for.
type Pause struct {
	Node    string `json:"node"`
	Payload any    `json:"payload"`
}

// Checkpoint is everything needed to continue a run later, from another process.
type Checkpoint[S any] struct {
	Thread    string    `json:"thread"`
	Next      string    `json:"next"`
	State     S         `json:"state"`
	Steps     []Step    `json:"steps"`
	Pause     *Pause    `json:"pause,omitzero"`
	Done      bool      `json:"done"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Paused reports whether the run is waiting for a Resume.
func (cp *Checkpoint[S]) Paused() bool { return cp.Pause != nil }

// Store persists checkpoints by thread id.
type Store[S any] interface {
	Load(ctx context.Context, thread string) (*Checkpoint[S], error)
	Save(ctx context.Context, cp *Checkpoint[S]) error
}

// ErrNotFound is returned by a Store when the thread does not exist.
var ErrNotFound = errors.New("graph: thread not found")

// ErrNotPaused is returned by Resume when the thread has nothing to resume.
var ErrNotPaused = errors.New("graph: thread is not paused")

// ErrDone is returned by Continue when the thread already reached End.
var ErrDone = errors.New("graph: thread is done")

// Phase is where a node is in its life when an Event is emitted.
type Phase string

// Phases of an Event.
const (
	PhaseStart  Phase = "start"
	PhaseDone   Phase = "done"
	PhasePaused Phase = "paused"
	PhaseFailed Phase = "failed"
	PhaseEnd    Phase = "end"
)

// Event is one observation of a run, for logs, dashboards or a browser watching live.
// State is the state after the node ran (or before, on PhaseStart).
type Event[S any] struct {
	Thread string        `json:"thread"`
	Node   string        `json:"node"`
	Phase  Phase         `json:"phase"`
	Next   string        `json:"next,omitempty"`
	Took   time.Duration `json:"took,omitempty"`
	Error  string        `json:"error,omitempty"`
	Pause  *Pause        `json:"pause,omitempty"`
	State  S             `json:"state"`
}

// Runner executes a Graph against a Store.
type Runner[S any] struct {
	graph   *Graph[S]
	store   Store[S]
	log     *slog.Logger
	observe func(Event[S])
	pace    time.Duration
}

// Option configures a Runner.
type Option[S any] func(*Runner[S])

// WithLogger makes the Runner log one record per executed node.
func WithLogger[S any](l *slog.Logger) Option[S] {
	return func(r *Runner[S]) { r.log = l }
}

// WithObserver calls fn on every phase of every node, synchronously. Keep it fast.
func WithObserver[S any](fn func(Event[S])) Option[S] {
	return func(r *Runner[S]) { r.observe = fn }
}

// WithPace waits d after every completed node, so a run that code would finish in
// milliseconds can be watched by a person. It changes nothing about what the run does.
func WithPace[S any](d time.Duration) Option[S] {
	return func(r *Runner[S]) { r.pace = d }
}

// NewRunner validates g and binds it to store.
func NewRunner[S any](g *Graph[S], store Store[S], opts ...Option[S]) (*Runner[S], error) {
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("graph: invalid graph: %w", err)
	}
	r := &Runner[S]{graph: g, store: store, log: slog.New(slog.DiscardHandler), observe: func(Event[S]) {}}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Start runs a new thread from the entry node until End or the first Interrupt.
func (r *Runner[S]) Start(ctx context.Context, thread string, initial S) (*Checkpoint[S], error) {
	cp := &Checkpoint[S]{Thread: thread, Next: r.graph.entry, State: initial}
	return r.run(ctx, cp)
}

// Resume continues a paused thread, handing value to the node that interrupted.
func (r *Runner[S]) Resume(ctx context.Context, thread string, value any) (*Checkpoint[S], error) {
	cp, err := r.store.Load(ctx, thread)
	if err != nil {
		return nil, err
	}
	if !cp.Paused() {
		return nil, fmt.Errorf("%w: %s", ErrNotPaused, thread)
	}
	ctx = context.WithValue(ctx, resumeKey, &resumeBox{value: value})
	return r.run(ctx, cp)
}

// Continue picks up a thread whose last node failed, from that node. The checkpoint saved
// after the previous successful node is the retry point; nothing is re-executed. This is
// how a run survives an expired token or a provider outage.
func (r *Runner[S]) Continue(ctx context.Context, thread string) (*Checkpoint[S], error) {
	cp, err := r.store.Load(ctx, thread)
	if err != nil {
		return nil, err
	}
	switch {
	case cp.Done:
		return nil, fmt.Errorf("%w: %s", ErrDone, thread)
	case cp.Paused():
		return nil, fmt.Errorf("graph: thread %s is paused; use Resume", thread)
	}
	return r.run(ctx, cp)
}

// run executes nodes from cp.Next until End or an Interrupt, saving after each one.
func (r *Runner[S]) run(ctx context.Context, cp *Checkpoint[S]) (*Checkpoint[S], error) {
	ctx = context.WithValue(ctx, threadKey, cp.Thread)
	log := r.log.With("thread", cp.Thread)

	for cp.Next != End {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := cp.Next
		node, ok := r.graph.nodes[name]
		if !ok {
			return nil, fmt.Errorf("graph: unknown node %q", name)
		}

		r.observe(Event[S]{Thread: cp.Thread, Node: name, Phase: PhaseStart, State: cp.State})
		started := time.Now()
		err := node(ctx, &cp.State)
		took := time.Since(started)

		var pause *interruptError
		if errors.As(err, &pause) {
			cp.Pause = &Pause{Node: name, Payload: pause.payload}
			log.InfoContext(ctx, "paused", "node", name, "took", took)
			r.observe(Event[S]{Thread: cp.Thread, Node: name, Phase: PhasePaused, Took: took, Pause: cp.Pause, State: cp.State})
			return cp, r.save(ctx, cp)
		}
		if err != nil {
			r.observe(Event[S]{Thread: cp.Thread, Node: name, Phase: PhaseFailed, Took: took, Error: err.Error(), State: cp.State})
			return nil, fmt.Errorf("node %s: %w", name, err)
		}

		cp.Pause = nil
		cp.Steps = append(cp.Steps, Step{Node: name, At: started.UTC(), Took: took})
		if cp.Next, err = r.graph.next(name, &cp.State); err != nil {
			return nil, err
		}
		log.InfoContext(ctx, "step", "node", name, "next", cp.Next, "took", took)
		r.observe(Event[S]{Thread: cp.Thread, Node: name, Phase: PhaseDone, Next: cp.Next, Took: took, State: cp.State})
		if err := r.save(ctx, cp); err != nil {
			return nil, err
		}
		if r.pace > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(r.pace):
			}
		}
	}
	cp.Done = true
	r.observe(Event[S]{Thread: cp.Thread, Node: End, Phase: PhaseEnd, State: cp.State})
	return cp, r.save(ctx, cp)
}

func (r *Runner[S]) save(ctx context.Context, cp *Checkpoint[S]) error {
	cp.UpdatedAt = time.Now().UTC()
	if err := r.store.Save(ctx, cp); err != nil {
		return fmt.Errorf("graph: save checkpoint %s: %w", cp.Thread, err)
	}
	return nil
}
