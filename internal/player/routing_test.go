package player

import (
	"strings"
	"testing"

	"github.com/mateusveloso/graphplay/internal/world"
)

func TestRouteAfterObserve(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.MaxDeaths, cfg.StuckTurns, cfg.MaxRiddleAttempts, cfg.MaxTurns = 3, 8, 3, 60
	room := func(assessed bool, riddle string) State {
		s := State{Turn: 5, LastProgress: 5, Obs: world.Observation{Room: "r", Riddle: riddle, Inventory: []string{"lantern"}}}
		k := s.here()
		k.Assessed, k.AssessedWith = assessed, []string{"lantern"}
		return s
	}
	tests := []struct {
		name  string
		state State
		want  string
	}{
		{"won", State{Won: true}, "finalize"},
		{"stopped", State{Stopped: true}, "finalize"},
		{"turn limit", State{Turn: 60}, "finalize"},
		{"too many deaths", State{Deaths: 3, Obs: world.Observation{Room: "r"}}, "gate"},
		{"stuck", State{Turn: 20, LastProgress: 12, Obs: world.Observation{Room: "r"}}, "gate"},
		{"riddle attempts spent", withTried(room(true, "?"), "answer a", "answer b", "answer c"), "gate"},
		{"riddle pending", room(true, "?"), "solve"},
		{"new room", room(false, ""), "assess"},
		{"assessed room", room(true, ""), "cheap_move"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := routeAfterObserve(cfg)(&tt.state); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func withTried(s State, cmds ...string) State {
	for _, c := range cmds {
		s.Tried = appendTo(s.Tried, s.Obs.Room, c)
	}
	return s
}

func TestInventoryChangeInvalidatesAssessment(t *testing.T) {
	t.Parallel()
	s := State{Obs: world.Observation{Room: "r"}}
	k := s.here()
	k.Assessed = true
	if !s.assessed() {
		t.Fatal("assessed with the same (empty) inventory")
	}
	s.Obs.Inventory = []string{"coin"}
	if s.assessed() {
		t.Fatal("a new item must trigger a fresh assessment")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	bad := testConfig(t)
	bad.RiskThreshold, bad.MaxDeaths, bad.Provider = 1.5, 0, "gemini"
	err := bad.Validate()
	for _, want := range []string{"PLAY_RISK_THRESHOLD", "PLAY_MAX_DEATHS", "PLAY_PROVIDER"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %s reported, got %v", want, err)
		}
	}
	if err := testConfig(t).Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}
