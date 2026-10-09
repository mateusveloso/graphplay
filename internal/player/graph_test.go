package player

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/world"
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
	if got := s.Fatal["start"]; len(got) != 1 || got[0].Command != "go east" {
		t.Fatalf("fatal command not remembered: %v", s.Fatal)
	}
	for _, c := range s.Commands {
		if c.String() == "go east" {
			t.Fatalf("the fatal command must be rolled back from the log: %v", s.Commands)
		}
	}
	var died, won bool
	for _, e := range s.Ledger {
		died = died || e.Died
		won = won || (e.Command == "go north" && e.Progress)
	}
	if !died || !won {
		t.Fatalf("want a recorded death then a winning move, got %+v", s.Ledger)
	}
}

func TestDecisionModelRiskStopsCodeFromWalkingIntoTheDark(t *testing.T) {
	t.Parallel()
	// Jev says east is lethal; code never tries it, so there is no death at all.
	decider := riskBy(map[string]float64{"danger_east": 0.95})
	_, cp := run(t, tiny(), script(), decider, testConfig(t))

	if cp.State.Deaths != 0 || !cp.State.Won {
		t.Fatalf("want a clean win, got deaths=%d won=%v", cp.State.Deaths, cp.State.Won)
	}
	if k := cp.State.Map["start"]; k.Danger["east"] != 0.95 || k.Risk["east"] < 0.9 || !k.Assessed {
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
	decider := riskBy(map[string]float64{"danger_east": 0.9, "danger_north": 0.9})
	model := script(Proposal{Commands: []string{"dance", "go west", "go north"}})

	_, cp := run(t, tiny(), model, decider, testConfig(t))

	if !cp.State.Won {
		t.Fatalf("want a win via the proposal, got %+v", cp.State.Ledger)
	}
	// Code looks around on arrival, then admits it has nothing and the proposal wins.
	last := cp.State.Ledger[len(cp.State.Ledger)-1]
	if last.Layer != LayerSmall || last.Command != "go north" {
		t.Fatalf("want the small model credited with 'go north', got %+v", last)
	}
}

func TestDecisionModelPicksAmongSeveralLegalProposals(t *testing.T) {
	t.Parallel()
	decider := riskBy(map[string]float64{"danger_east": 0.9, "danger_north": 0.9})
	decider.answer = chooseExactly(decider.answer, "go north") // confidence 0.9: no escalation
	model := script(Proposal{Commands: []string{"go east", "go north"}})

	_, cp := run(t, tiny(), model, decider, testConfig(t))

	if !cp.State.Won || cp.State.Deaths != 0 {
		t.Fatalf("want a clean win, got %+v", cp.State.Ledger)
	}
	if last := cp.State.Ledger[len(cp.State.Ledger)-1]; last.Layer != LayerDecision || !strings.Contains(last.Note, "over 2 proposals") {
		t.Fatalf("want the decision model credited, got %+v", last)
	}
	if model.asked(Pick{}) != 0 {
		t.Fatal("a confident decision must not be escalated")
	}
}

func TestAnUnsureDecisionIsEscalatedToTheLargeModel(t *testing.T) {
	t.Parallel()
	decider := riskBy(map[string]float64{"danger_east": 0.9, "danger_north": 0.9})
	decider.answer = chooseExactly(decider.answer, "go east") // wrong, and at 0.9 it would die
	model := script(Proposal{Commands: []string{"go east", "go north"}}, Pick{Command: "go north", Reasoning: "the inscription"})
	cfg := testConfig(t)
	cfg.RankMinConfidence = 0.95 // force the escalation

	_, cp := run(t, tiny(), model, decider, cfg)

	if !cp.State.Won || cp.State.Deaths != 0 {
		t.Fatalf("want a clean win through the large model, got %+v", cp.State.Ledger)
	}
	last := cp.State.Ledger[len(cp.State.Ledger)-1]
	if last.Layer != LayerLarge || !strings.Contains(last.Note, "unsure (0.90)") || !strings.Contains(last.Note, "inscription") {
		t.Fatalf("want the large model credited with its reasoning, got %+v", last)
	}
}

func TestStuckTurnsHandControlToAHuman(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.StuckTurns = 3 // the first look counts as progress; the useless proposals do not
	// Nothing is lethal, but the proposal keeps repeating useless looks until patience runs out.
	model := script(Proposal{Commands: []string{"look"}}, Proposal{Commands: []string{"look"}}, Proposal{Commands: []string{"look"}})
	decider := riskBy(map[string]float64{"danger_east": 0.9, "danger_north": 0.9})

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

func TestCodeLooksForHiddenExitsBeforeAskingAnyModel(t *testing.T) {
	t.Parallel()
	// caverns: the way to the crypt is hidden in the gallery and only "look" reveals it.
	// The two riddles are the only model turns a safe-everywhere decider should allow.
	decider := riskBy(map[string]float64{"protected_north": 0.95}) // every danger covered
	model := script(RiddleAnswer{Answer: "footsteps"}, RiddleAnswer{Answer: "map"})

	_, cp := run(t, cellarOrCaverns(t, "caverns"), model, decider, testConfig(t))

	s := &cp.State
	if !s.Won {
		t.Fatalf("want a win, got turn=%d deaths=%d paused=%v", s.Turn, s.Deaths, cp.Paused())
	}
	by := layers(s)
	if by[LayerLarge] != 2 || by[LayerSmall] != 0 {
		t.Fatalf("want exactly the two riddles on the large model, got %v", by)
	}
	looked := 0
	for _, e := range s.Ledger {
		if e.Command == "look" {
			looked++
		}
	}
	if looked == 0 || !s.Map["gallery"].Looked {
		t.Fatalf("code should have looked around the gallery: %+v", s.Ledger)
	}
	if by[LayerCode] > 40 {
		t.Fatalf("a look per room must not turn into a tour: %d code turns", by[LayerCode])
	}
}

func TestARefusedExitIsReconsideredWhenTheInventoryChanges(t *testing.T) {
	t.Parallel()
	// The only way on is a dark room and the lantern is behind you. Code must refuse the
	// dark exit, fetch the lantern, come back, get a fresh assessment and then go.
	w := world.World{
		ID: "lantern-behind", Start: "fork", GoalText: "Won.",
		Items: map[string]world.Item{"lantern": {Name: "lantern"}},
		Rooms: map[string]*world.Room{
			"fork":   {Name: "Fork", Description: "Dark to the north; a closet south.", Exits: map[string]string{"north": "dark", "south": "closet"}},
			"closet": {Name: "Closet", Description: "A lantern.", Items: []string{"lantern"}, Exits: map[string]string{"north": "fork"}},
			"dark":   {Name: "Dark", Description: "Dark.", Dark: true, Death: "You fall.", Exits: map[string]string{"south": "fork", "north": "goal"}},
			"goal":   {Name: "Goal", Description: "Gold.", Goal: true, Exits: map[string]string{"south": "dark"}},
		},
	}
	// North is dangerous; the player is protected only once the state lists the lantern.
	decider := &fakeDecider{answer: func(state string, qs map[string]jev.Question) map[string]jev.Answer {
		out := make(map[string]jev.Answer, len(qs))
		for key, q := range qs {
			switch {
			case q.Type == "choice":
				out[key] = jev.Answer{Choice: "ordinary", Confidence: 0.8}
			case key == "danger_north":
				out[key] = noul(0.9)
			case key == "protected_north" && strings.Contains(state, "Inventory: lantern"):
				out[key] = noul(0.95)
			default:
				out[key] = noul(0.05)
			}
		}
		return out
	}}

	_, cp := run(t, w, script(), decider, testConfig(t))

	s := &cp.State
	if !s.Won || s.Deaths != 0 {
		t.Fatalf("want a clean win, got won=%v deaths=%d ledger=%+v", s.Won, s.Deaths, s.Ledger)
	}
	if h := s.Map["fork"].History; len(h) != 2 || h[0].Risk["north"] < 0.3 || h[1].Risk["north"] > 0.3 {
		t.Fatalf("want two assessments of the fork, risky then safe, got %+v", h)
	}
}
