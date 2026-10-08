package triage

import (
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	p := func(v float64) *float64 { return &v }

	tests := []struct {
		name  string
		route func(*State) string
		state State
		want  string
	}{
		{"classify: confident no code", routeAfterClassify(cfg), State{Prior: Prior{NeedsCode: p(0.05)}}, "writer"},
		{"classify: unsure", routeAfterClassify(cfg), State{Prior: Prior{NeedsCode: p(0.5)}}, "planner"},
		{"classify: no decision", routeAfterClassify(cfg), State{}, "planner"},
		{"check: uncertain", routeAfterCheck(cfg), State{Verdict: VerdictUncertain, CriticRounds: 1}, "critic"},
		{"check: pass", routeAfterCheck(cfg), State{Verdict: VerdictPass, CriticRounds: 1}, "gate"},
		{"check: reject within budget", routeAfterCheck(cfg), State{Verdict: VerdictReject, CriticRounds: 1}, "writer"},
		{"check: reject, budget spent", routeAfterCheck(cfg), State{Verdict: VerdictReject, CriticRounds: 2}, "gate"},
		{"critic: approved", routeAfterCritic(cfg), State{Reviews: []Review{{Approved: true}}, CriticRounds: 1}, "gate"},
		{"critic: rejected within budget", routeAfterCritic(cfg), State{Reviews: []Review{{}}, CriticRounds: 1}, "writer"},
		{"critic: rejected, budget spent", routeAfterCritic(cfg), State{Reviews: []Review{{}}, CriticRounds: 2}, "gate"},
		{"gate: approved", routeAfterGate, State{Approved: true}, "finalize"},
		{"gate: rejected", routeAfterGate, State{}, "writer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.route(&tt.state); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	bad := testConfig(t)
	bad.RubricFail, bad.RubricPass = 0.9, 0.8
	bad.MaxCriticRounds = 0
	err := bad.Validate()
	if err == nil {
		t.Fatal("want validation errors")
	}
	for _, want := range []string{"TRIAGE_RUBRIC_FAIL", "TRIAGE_MAX_CRITIC_ROUNDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
	if err := testConfig(t).Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}
