package baseline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/world"
)

type scriptedModel struct{ commands []string }

func (m *scriptedModel) Ask(_ context.Context, _, user string, out any) error {
	if len(m.commands) == 0 {
		return fmt.Errorf("out of script; last prompt:\n%s", user)
	}
	c := m.commands[0]
	m.commands = m.commands[1:]
	raw, _ := json.Marshal(Move{Command: c, Thinking: "scripted"})
	return json.Unmarshal(raw, out)
}

type firstOption struct{}

func (firstOption) Decide(_ context.Context, _ string, qs map[string]jev.Question) (map[string]jev.Answer, error) {
	opts := qs["next"].Criteria.(map[string]string)
	best := ""
	for k := range opts {
		if best == "" || k < best {
			best = k
		}
	}
	return map[string]jev.Answer{"next": {Choice: best, Confidence: 0.5}}, nil
}

func cellar(t *testing.T) world.World {
	t.Helper()
	w, err := world.Load("cellar")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestLLMBaselineReloadsOnDeathAndCanWin(t *testing.T) {
	t.Parallel()
	model := &scriptedModel{commands: []string{
		"go north", "go north", // dies in the dark, reloaded to the corridor
		"go south", "take lantern", "go north", "go north", "go north", "go east", "answer echo", "go east",
	}}
	res, err := PlayLLM(context.Background(), cellar(t), model, Limits{MaxTurns: 20, MaxDeaths: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Won || res.Deaths != 1 || res.Turns != 10 {
		t.Fatalf("want a win after one death in 10 turns, got %+v", res)
	}
	report, _ := res.Report()
	if !strings.Contains(report, "(died, reloaded)") {
		t.Fatalf("report should show the death:\n%s", report)
	}
}

func TestDecisionBaselineCannotAnswerRiddlesAndStopsAtTheBudget(t *testing.T) {
	t.Parallel()
	res, err := PlayDecision(context.Background(), cellar(t), firstOption{}, Limits{MaxTurns: 12, MaxDeaths: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Won || res.Turns != 12 {
		t.Fatalf("a decision model alone cannot type 'echo'; got %+v", res)
	}
}
