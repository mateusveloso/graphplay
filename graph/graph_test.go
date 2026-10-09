package graph_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mateusveloso/graphplay/graph"
)

type state struct {
	Visited []string
	OK      bool
}

type answer struct{ OK bool }

func visit(name string) graph.Node[state] {
	return func(_ context.Context, s *state) error {
		s.Visited = append(s.Visited, name)
		return nil
	}
}

// ask pauses with a question and records the answer when resumed.
func ask(ctx context.Context, s *state) error {
	s.Visited = append(s.Visited, "ask")
	a, err := graph.Interrupt[answer](ctx, "continue?")
	if err != nil {
		return err
	}
	s.OK = a.OK
	return nil
}

func testGraph() *graph.Graph[state] {
	return graph.New[state]().
		Node("a", visit("a")).
		Node("ask", ask).
		Node("b", visit("b")).
		Entry("a").
		Edge("a", "ask").
		Route("ask", func(s *state) string {
			if s.OK {
				return "b"
			}
			return "a"
		}, "a", "b").
		Edge("b", graph.End)
}

func newRunner(t *testing.T) *graph.Runner[state] {
	t.Helper()
	r, err := graph.NewRunner(testGraph(), &graph.MemoryStore[state]{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunPausesAtInterruptAndResumes(t *testing.T) {
	t.Parallel()
	r := newRunner(t)
	ctx := context.Background()

	cp, err := r.Start(ctx, "t1", state{})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Paused() || cp.Pause.Node != "ask" || cp.Pause.Payload != "continue?" {
		t.Fatalf("want pause at ask with payload, got %+v", cp.Pause)
	}

	cp, err = r.Resume(ctx, "t1", answer{OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done {
		t.Fatalf("want done, got next=%q", cp.Next)
	}
	want := []string{"a", "ask", "ask", "b"}
	if !slices.Equal(cp.State.Visited, want) {
		t.Fatalf("visited %v, want %v", cp.State.Visited, want)
	}
	// The paused execution of "ask" is not a step; only completed nodes are recorded.
	if got := len(cp.Steps); got != 3 {
		t.Fatalf("want 3 recorded steps, got %d", got)
	}
}

func TestResumeValueIsConsumedOnce(t *testing.T) {
	t.Parallel()
	r := newRunner(t)
	ctx := context.Background()
	if _, err := r.Start(ctx, "t2", state{}); err != nil {
		t.Fatal(err)
	}
	// A negative answer routes back to "a" and reaches "ask" again, which must pause
	// rather than reuse the old answer.
	cp, err := r.Resume(ctx, "t2", answer{OK: false})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Paused() {
		t.Fatal("second ask should pause again")
	}
}

func TestResumeErrors(t *testing.T) {
	t.Parallel()
	r := newRunner(t)
	ctx := context.Background()

	t.Run("unknown thread", func(t *testing.T) {
		_, err := r.Resume(ctx, "nope", answer{})
		if !errors.Is(err, graph.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
	t.Run("wrong value type", func(t *testing.T) {
		if _, err := r.Start(ctx, "t3", state{}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Resume(ctx, "t3", "yes"); err == nil {
			t.Fatal("want a type error from Interrupt")
		}
	})
	t.Run("finished thread", func(t *testing.T) {
		if _, err := r.Start(ctx, "t4", state{}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Resume(ctx, "t4", answer{OK: true}); err != nil {
			t.Fatal(err)
		}
		_, err := r.Resume(ctx, "t4", answer{OK: true})
		if !errors.Is(err, graph.ErrNotPaused) {
			t.Fatalf("want ErrNotPaused, got %v", err)
		}
	})
}

func TestContinueRetriesTheFailedNode(t *testing.T) {
	t.Parallel()
	attempts := 0
	flaky := func(_ context.Context, s *state) error {
		attempts++
		if attempts == 1 {
			return errors.New("provider down")
		}
		s.Visited = append(s.Visited, "flaky")
		return nil
	}
	g := graph.New[state]().
		Node("a", visit("a")).
		Node("flaky", flaky).
		Entry("a").
		Edge("a", "flaky").
		Edge("flaky", graph.End)
	store := &graph.MemoryStore[state]{}
	r, _ := graph.NewRunner(g, store)
	ctx := context.Background()

	if _, err := r.Start(ctx, "t", state{}); err == nil {
		t.Fatal("first attempt should fail")
	}
	cp, err := r.Continue(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done || !slices.Equal(cp.State.Visited, []string{"a", "flaky"}) {
		t.Fatalf("want a completed run without re-running a, got %+v", cp.State)
	}
	if _, err := r.Continue(ctx, "t"); !errors.Is(err, graph.ErrDone) {
		t.Fatalf("want ErrDone, got %v", err)
	}
}

func TestObserverSeesEveryPhase(t *testing.T) {
	t.Parallel()
	var phases []string
	r, err := graph.NewRunner(testGraph(), &graph.MemoryStore[state]{}, graph.WithObserver(func(e graph.Event[state]) {
		phases = append(phases, e.Node+":"+string(e.Phase))
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Start(ctx, "t", state{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(ctx, "t", answer{OK: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a:start", "a:done", "ask:start", "ask:paused", "ask:start", "ask:done", "b:start", "b:done", "__end__:end"}
	if !slices.Equal(phases, want) {
		t.Fatalf("phases %v, want %v", phases, want)
	}
}

func TestBeforeNodeHookRunsBeforeEveryNodeAndCanAbort(t *testing.T) {
	t.Parallel()
	var seen []string
	r, _ := graph.NewRunner(testGraph(), &graph.MemoryStore[state]{}, graph.WithBeforeNode[state](func(_ context.Context, node string) error {
		seen = append(seen, node)
		if node == "b" {
			return errors.New("stop here")
		}
		return nil
	}))
	ctx := context.Background()
	if _, err := r.Start(ctx, "t", state{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(ctx, "t", answer{OK: true}); err == nil {
		t.Fatal("want the hook's error")
	}
	if !slices.Equal(seen, []string{"a", "ask", "ask", "b"}) {
		t.Fatalf("hook saw %v", seen)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		graph *graph.Graph[state]
		want  string
	}{
		{
			name:  "dangling edge",
			graph: graph.New[state]().Node("a", visit("a")).Entry("a").Edge("a", "ghost"),
			want:  `unknown node "ghost"`,
		},
		{
			name:  "no outgoing edge",
			graph: graph.New[state]().Node("a", visit("a")).Entry("a"),
			want:  "no outgoing edge",
		},
		{
			name:  "missing entry",
			graph: graph.New[state]().Node("a", visit("a")).Edge("a", graph.End),
			want:  "entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.graph.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestRouterMustReturnDeclaredTarget(t *testing.T) {
	t.Parallel()
	g := graph.New[state]().
		Node("a", visit("a")).
		Entry("a").
		Route("a", func(*state) string { return "elsewhere" }, graph.End)
	r, err := graph.NewRunner(g, &graph.MemoryStore[state]{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(context.Background(), "t", state{}); err == nil {
		t.Fatal("want undeclared target error")
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := graph.FileStore[state]{Dir: t.TempDir()}
	r, err := graph.NewRunner(testGraph(), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Start(ctx, "disk", state{}); err != nil {
		t.Fatal(err)
	}
	// A fresh runner over the same directory sees the paused thread.
	again, _ := graph.NewRunner(testGraph(), store)
	cp, err := again.Resume(ctx, "disk", answer{OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done {
		t.Fatal("want the run to finish after resume from disk")
	}
}

func TestMermaidIsDeterministic(t *testing.T) {
	t.Parallel()
	a, b := testGraph().Mermaid(), testGraph().Mermaid()
	if a != b {
		t.Fatal("diagram must not depend on map iteration order")
	}
	for _, want := range []string{"a --> ask;", "ask -.-> a;", "ask -.-> b;", "b --> __end__;"} {
		if !strings.Contains(a, want) {
			t.Fatalf("diagram missing %q:\n%s", want, a)
		}
	}
}
