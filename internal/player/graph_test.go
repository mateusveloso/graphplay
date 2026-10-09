package player

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCodeAloneExploresTheCellarAndTheLargeModelOnlySolvesTheRiddle(t *testing.T) {
	t.Parallel()
	model := script(RiddleAnswer{Answer: "Echo", Reasoning: "it repeats"})

	_, cp := run(t, cellar(t), model, noDecider(), testConfig(t))

	s := &cp.State
	if !cp.Done || !s.Won {
		t.Fatalf("want a won game, got done=%v won=%v deaths=%d turn=%d", cp.Done, s.Won, s.Deaths, s.Turn)
	}
	by := layers(s)
	if by[LayerLarge] != 1 || by[LayerSmall] != 0 || by[LayerHuman] != 0 {
		t.Fatalf("want exactly one large-model turn and no others, got %v", by)
	}
	if model.asked(Proposal{}) != 0 {
		t.Fatal("the small model must not run while code has safe moves")
	}
	report, err := os.ReadFile(s.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "| large-llm | 1 |") {
		t.Fatalf("report lacks the layer table:\n%s", report)
	}
}

func TestDeathReloadsAndIsRemembered(t *testing.T) {
	t.Parallel()
	_, cp := run(t, tiny(), script(), noDecider(), testConfig(t))

	s := &cp.State
	if !s.Won || s.Deaths != 1 {
		t.Fatalf("want one death then victory, got won=%v deaths=%d", s.Won, s.Deaths)
	}
	if got := s.Fatal["start"]; len(got) != 1 || got[0] != "go east" {
		t.Fatalf("fatal command not remembered: %v", s.Fatal)
	}
	if len(s.Commands) != 1 || s.Commands[0].String() != "go north" {
		t.Fatalf("the fatal command must be rolled back from the log: %v", s.Commands)
	}
	if !s.Ledger[0].Died || !s.Ledger[1].Progress {
		t.Fatalf("want a recorded death then a winning move, got %+v", s.Ledger)
	}
}

func TestDecisionModelRiskStopsCodeFromWalkingIntoTheDark(t *testing.T) {
	t.Parallel()
	// Jev says east is lethal; code never tries it, so there is no death at all.
	decider := riskBy(map[string]float64{"exit_east": 0.95})
	_, cp := run(t, tiny(), script(), decider, testConfig(t))

	if cp.State.Deaths != 0 || !cp.State.Won {
		t.Fatalf("want a clean win, got deaths=%d won=%v", cp.State.Deaths, cp.State.Won)
	}
	if k := cp.State.Map["start"]; k.Risk["east"] != 0.95 || !k.Assessed {
		t.Fatalf("assessment not stored: %+v", k)
	}
}

func TestRiddleAttemptsAreBoundedThenAHumanIsAsked(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.MaxRiddleAttempts = 2
	model := script(RiddleAnswer{Answer: "map"}, RiddleAnswer{Answer: "keyboard"})

	r, cp := run(t, locked(), model, noDecider(), cfg)

	if !cp.Paused() || cp.Pause.Node != "gate" {
		t.Fatalf("want the human gate after %d wrong answers, got %+v", cfg.MaxRiddleAttempts, cp.Pause)
	}
	screen := cp.Pause.Payload.(Screen)
	if !strings.Contains(screen.Why, "riddle") || screen.Obs.Riddle == "" {
		t.Fatalf("screen should explain the riddle is stuck: %+v", screen)
	}

	cp, err := r.Resume(context.Background(), t.Name(), Decision{Command: "answer piano"})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done || !cp.State.Won || layers(&cp.State)[LayerHuman] != 1 {
		t.Fatalf("human answer should win: done=%v won=%v layers=%v", cp.Done, cp.State.Won, layers(&cp.State))
	}
}

func TestHumanCanStopTheRun(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.MaxRiddleAttempts = 1
	r, _ := run(t, locked(), script(RiddleAnswer{Answer: "nope"}), noDecider(), cfg)

	cp, err := r.Resume(context.Background(), t.Name(), Decision{Stop: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done || cp.State.Won || !cp.State.Stopped {
		t.Fatalf("want a stopped, unfinished game: %+v", cp.State)
	}
}

func TestSmallModelProposesWhenCodeHasNoSafeMoveAndRankFiltersNonsense(t *testing.T) {
	t.Parallel()
	// Both exits judged lethal: code refuses to explore, the small model proposes, the
	// legal filter drops "dance" and "go west", one candidate remains, no decision needed.
	decider := riskBy(map[string]float64{"exit_east": 0.9, "exit_north": 0.9})
	model := script(Proposal{Commands: []string{"dance", "go west", "go north"}})

	_, cp := run(t, tiny(), model, decider, testConfig(t))

	if !cp.State.Won {
		t.Fatalf("want a win via the proposal, got %+v", cp.State.Ledger)
	}
	e := cp.State.Ledger[0]
	if e.Layer != LayerSmall || e.Command != "go north" {
		t.Fatalf("want the small model credited with 'go north', got %+v", e)
	}
}

func TestDecisionModelPicksAmongSeveralLegalProposals(t *testing.T) {
	t.Parallel()
	decider := riskBy(map[string]float64{"exit_east": 0.9, "exit_north": 0.9})
	decider.answer = chooseExactly(decider.answer, "go north")
	model := script(Proposal{Commands: []string{"go east", "go north"}})

	_, cp := run(t, tiny(), model, decider, testConfig(t))

	if !cp.State.Won || cp.State.Deaths != 0 {
		t.Fatalf("want a clean win, got %+v", cp.State.Ledger)
	}
	if e := cp.State.Ledger[0]; e.Layer != LayerDecision || !strings.Contains(e.Note, "over 2 proposals") {
		t.Fatalf("want the decision model credited, got %+v", e)
	}
}

func TestStuckTurnsHandControlToAHuman(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.StuckTurns = 2
	// Nothing is lethal, but the proposal keeps repeating useless looks until patience runs out.
	model := script(Proposal{Commands: []string{"look"}}, Proposal{Commands: []string{"look"}}, Proposal{Commands: []string{"look"}})
	decider := riskBy(map[string]float64{"exit_east": 0.9, "exit_north": 0.9})

	_, cp := run(t, tiny(), model, decider, cfg)

	if !cp.Paused() || cp.Pause.Node != "gate" {
		t.Fatalf("want the gate after %d idle turns, got %+v", cfg.StuckTurns, cp.Pause)
	}
	if why := cp.Pause.Payload.(Screen).Why; !strings.Contains(why, "no progress") {
		t.Fatalf("why=%q", why)
	}
}

func TestMaxTurnsEndsTheGameUnfinished(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.MaxTurns = 1
	_, cp := run(t, cellar(t), script(), noDecider(), cfg)
	if !cp.Done || cp.State.Won || cp.State.Turn != 1 {
		t.Fatalf("want an unfinished game after one turn: turn=%d won=%v", cp.State.Turn, cp.State.Won)
	}
}
