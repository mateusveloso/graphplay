package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mateusveloso/graph-issue-triage/graph"
	"github.com/mateusveloso/graph-issue-triage/internal/github"
	"github.com/mateusveloso/graph-issue-triage/internal/jev"
	"github.com/mateusveloso/graph-issue-triage/internal/llm"
)

// fakeModel answers each Ask with the next scripted value whose type matches out.
// It records every prompt so tests can assert what a node was told.
type fakeModel struct {
	scripts map[reflect.Type][]any
	calls   []call
}

type call struct {
	out    reflect.Type
	system string
	user   string
}

func script(values ...any) *fakeModel {
	m := &fakeModel{scripts: make(map[reflect.Type][]any)}
	for _, v := range values {
		t := reflect.TypeOf(v)
		m.scripts[t] = append(m.scripts[t], v)
	}
	return m
}

func (m *fakeModel) Ask(_ context.Context, system, user string, out any) error {
	t := reflect.TypeOf(out).Elem()
	m.calls = append(m.calls, call{out: t, system: system, user: user})
	queue := m.scripts[t]
	if len(queue) == 0 {
		return fmt.Errorf("fake model: no scripted %s left", t.Name())
	}
	m.scripts[t] = queue[1:]
	raw, _ := json.Marshal(queue[0])
	return json.Unmarshal(raw, out)
}

// prompts returns the user prompts sent for a given output type, in order.
func (m *fakeModel) prompts(out any) []string {
	want := reflect.TypeOf(out)
	var ps []string
	for _, c := range m.calls {
		if c.out == want {
			ps = append(ps, c.user)
		}
	}
	return ps
}

func (m *fakeModel) asked(out any) int { return len(m.prompts(out)) }

// fakeDecider answers each Decide with the next scripted answer set; nil means "no decision".
type fakeDecider struct {
	answers []map[string]jev.Answer
	calls   []map[string]jev.Question
}

func decide(answers ...map[string]jev.Answer) *fakeDecider {
	return &fakeDecider{answers: answers}
}

func (d *fakeDecider) Decide(_ context.Context, _ string, qs map[string]jev.Question) (map[string]jev.Answer, error) {
	d.calls = append(d.calls, qs)
	if len(d.answers) == 0 || d.answers[0] == nil {
		if len(d.answers) > 0 {
			d.answers = d.answers[1:]
		}
		return nil, fmt.Errorf("fake decider: no decision")
	}
	a := d.answers[0]
	d.answers = d.answers[1:]
	return a, nil
}

func noul(p float64) jev.Answer { return jev.Answer{Noul: &p} }

func choice(option string, confidence float64) jev.Answer {
	return jev.Answer{Choice: option, Confidence: confidence}
}

func rubricAnswers(grounded, categoryMatches, actionable float64) map[string]jev.Answer {
	return map[string]jev.Answer{
		"grounded":         noul(grounded),
		"category_matches": noul(categoryMatches),
		"actionable":       noul(actionable),
	}
}

// fakeRepo is a two-file repository with one issue. collect fetches concurrently, so the
// call log is guarded.
type fakeRepo struct {
	files map[string]string

	mu    sync.Mutex
	calls []string
}

func (r *fakeRepo) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *fakeRepo) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newRepo() *fakeRepo {
	return &fakeRepo{files: map[string]string{
		"README.md":  "# demo\n",
		"src/app.py": "def main():\n    pass\n",
	}}
}

func (r *fakeRepo) Issue(_ context.Context, owner, repo string, number int) (github.Issue, error) {
	return github.Issue{
		Owner: owner, Repo: repo, Number: number,
		Title:  "App crashes on start",
		Body:   "Running `python -m app` raises TypeError.",
		Labels: []string{"bug"},
		URL:    fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, repo, number),
	}, nil
}

func (r *fakeRepo) Root(context.Context, string, string) ([]string, error) {
	return []string{"README.md", "src/"}, nil
}

func (r *fakeRepo) File(_ context.Context, _, _, path string) (string, error) {
	r.record("read:" + path)
	content, ok := r.files[path]
	if !ok {
		return "", fmt.Errorf("not found: %s", path)
	}
	return content, nil
}

func (r *fakeRepo) Search(_ context.Context, _, _, query string) ([]string, error) {
	r.record("search:" + query)
	var hits []string
	for path, content := range r.files {
		if strings.Contains(content, query) {
			hits = append(hits, path)
		}
	}
	slices.Sort(hits)
	return hits, nil
}

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg := FromEnv()
	cfg.StateDir = t.TempDir()
	cfg.MaxCriticRounds = 2
	cfg.MaxEvidenceRequests = 3
	return cfg
}

var (
	testPlan = Plan{
		Hypotheses: []string{"entrypoint signature changed"},
		Requests:   []EvidenceRequest{{Kind: "read_file", Target: "src/app.py", Reason: "entrypoint"}},
	}
	goodDraft = Triage{
		Category:     "bug",
		Summary:      "main() takes no args but is called with argv.",
		EvidenceUsed: []string{"src/app.py"},
		NextSteps:    []string{"accept argv in main()"},
		Confidence:   "high",
	}
	approve = Review{Approved: true, Feedback: "looks grounded"}
)

func rejectWith(feedback string) Review { return Review{Feedback: feedback} }

// run builds the graph with fakes and starts a thread.
func run(t *testing.T, model llm.Model, decider jev.Decider, cfg Config) (*graph.Runner[State], *graph.Checkpoint[State]) {
	t.Helper()
	r, err := graph.NewRunner(Build(llm.Single(model), newRepo(), decider, cfg), &graph.MemoryStore[State]{})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := r.Start(context.Background(), t.Name(), State{IssueRef: "octo/demo#7"})
	if err != nil {
		t.Fatal(err)
	}
	return r, cp
}

func gatePayload(t *testing.T, cp *graph.Checkpoint[State]) GatePayload {
	t.Helper()
	if !cp.Paused() || cp.Pause.Node != "gate" {
		t.Fatalf("want a pause at gate, got %+v", cp.Pause)
	}
	return cp.Pause.Payload.(GatePayload)
}
