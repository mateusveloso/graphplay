package player

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/mateusveloso/graphplay/graph"
	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/world"
)

// fakeModel answers each Ask with the next scripted value whose type matches out.
type fakeModel struct {
	scripts map[reflect.Type][]any
	calls   map[reflect.Type]int
}

func script(values ...any) *fakeModel {
	m := &fakeModel{scripts: map[reflect.Type][]any{}, calls: map[reflect.Type]int{}}
	for _, v := range values {
		m.scripts[reflect.TypeOf(v)] = append(m.scripts[reflect.TypeOf(v)], v)
	}
	return m
}

func (m *fakeModel) Ask(_ context.Context, _, _ string, out any) error {
	t := reflect.TypeOf(out).Elem()
	m.calls[t]++
	queue := m.scripts[t]
	if len(queue) == 0 {
		return fmt.Errorf("fake model: no scripted %s left", t.Name())
	}
	m.scripts[t] = queue[1:]
	raw, _ := json.Marshal(queue[0])
	return json.Unmarshal(raw, out)
}

func (m *fakeModel) asked(out any) int { return m.calls[reflect.TypeOf(out)] }

// fakeDecider answers with a function of the state and the questions, so a test can say
// "every exit is safe", "west is lethal" or "protected once the state mentions a lantern"
// without scripting call order.
type fakeDecider struct {
	answer func(state string, qs map[string]jev.Question) map[string]jev.Answer
	calls  int
}

func (d *fakeDecider) Decide(_ context.Context, state string, qs map[string]jev.Question) (map[string]jev.Answer, error) {
	d.calls++
	if d.answer == nil {
		return nil, fmt.Errorf("fake decider: no decision")
	}
	return d.answer(state, qs), nil
}

// riskBy answers every noul with the given probability per question key (default 0.05, so
// an unlisted exit is safe and unprotected) and picks the alphabetically first option of
// any choice. Keys are "danger_<dir>" and "protected_<dir>". Wrap with chooseExactly to
// steer a choice.
func riskBy(risk map[string]float64) *fakeDecider {
	return &fakeDecider{answer: func(_ string, qs map[string]jev.Question) map[string]jev.Answer {
		out := make(map[string]jev.Answer, len(qs))
		for key, q := range qs {
			switch q.Type {
			case "noul":
				p := 0.05
				if v, ok := risk[key]; ok {
					p = v
				}
				out[key] = jev.Answer{Noul: &p}
			case "choice":
				names := slices.Sorted(maps.Keys(q.Criteria.(map[string]string)))
				out[key] = jev.Answer{Choice: names[0], Confidence: 0.8}
			}
		}
		return out
	}}
}

func noDecider() *fakeDecider { return &fakeDecider{} }

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg := FromEnv()
	cfg.StateDir = t.TempDir()
	cfg.MaxTurns = 40
	return cfg
}

func cellar(t *testing.T) world.World { return cellarOrCaverns(t, "cellar") }

func cellarOrCaverns(t *testing.T, id string) world.World {
	t.Helper()
	w, err := world.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// tiny builds a two-exit world: start -> east is a lethal dark room, start -> north is the goal.
// Sorted exits make code try east first, so it dies once before it wins.
func tiny() world.World {
	return world.World{
		ID: "tiny", Start: "start", GoalText: "Won.",
		Items: map[string]world.Item{},
		Rooms: map[string]*world.Room{
			"start": {Name: "Start", Description: "Two doors.", Exits: map[string]string{"east": "pit", "north": "goal"}},
			"pit":   {Name: "Pit", Description: "Dark.", Dark: true, Death: "You fall.", Exits: map[string]string{"west": "start"}},
			"goal":  {Name: "Goal", Description: "Gold.", Goal: true, Exits: map[string]string{"south": "start"}},
		},
	}
}

// locked builds a world whose only exit is a riddle door.
func locked() world.World {
	return world.World{
		ID: "locked", Start: "cell", GoalText: "Free.",
		Items: map[string]world.Item{},
		Rooms: map[string]*world.Room{
			"cell": {Name: "Cell", Description: "A door with a riddle.", Exits: map[string]string{"north": "out"},
				Locks: map[string]world.Lock{"north": {Riddle: "What has keys but no locks?", Answer: "piano", Closed: "Locked.", Opened: "Open."}}},
			"out": {Name: "Out", Description: "Sky.", Goal: true, Exits: map[string]string{"south": "cell"}},
		},
	}
}

func run(t *testing.T, w world.World, model llm.Model, decider jev.Decider, cfg Config) (*graph.Runner[State], *graph.Checkpoint[State]) {
	t.Helper()
	r, err := graph.NewRunner(Build(w, llm.Single(model), decider, cfg), &graph.MemoryStore[State]{})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := r.Start(context.Background(), t.Name(), State{World: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	return r, cp
}

func layers(s *State) map[Layer]int {
	out := map[Layer]int{}
	for _, e := range s.Ledger {
		out[e.Layer]++
	}
	return out
}

// chooseExactly wraps a decider answer so every choice question returns want.
func chooseExactly(inner func(string, map[string]jev.Question) map[string]jev.Answer, want string) func(string, map[string]jev.Question) map[string]jev.Answer {
	return func(state string, qs map[string]jev.Question) map[string]jev.Answer {
		out := inner(state, qs)
		for key, q := range qs {
			if q.Type == "choice" {
				if _, ok := q.Criteria.(map[string]string)[want]; ok {
					out[key] = jev.Answer{Choice: want, Confidence: 0.9}
				}
			}
		}
		return out
	}
}

func noul(p float64) jev.Answer { return jev.Answer{Noul: &p} }
