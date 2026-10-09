// Package player is the agent: a graph that plays a text adventure, taking every decision
// at the cheapest layer that can take it.
package player

import (
	"slices"

	"github.com/mateusveloso/graphplay/internal/world"
)

// Layer names who decided a turn. The ledger counts turns per layer; that count is the
// point of the whole exercise.
type Layer string

// Layers, cheapest first.
const (
	LayerCode     Layer = "code"
	LayerDecision Layer = "decision-model"
	LayerSmall    Layer = "small-llm"
	LayerLarge    Layer = "large-llm"
	LayerHuman    Layer = "human"
)

// Choice is the command the current turn will execute and who picked it.
type Choice struct {
	Command string `json:"command"`
	Layer   Layer  `json:"layer"`
	Note    string `json:"note,omitempty"`
}

// Entry is one executed turn.
type Entry struct {
	Turn     int    `json:"turn"`
	Room     string `json:"room"`
	Command  string `json:"command"`
	Layer    Layer  `json:"layer"`
	Note     string `json:"note,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Progress bool   `json:"progress,omitempty"`
	Died     bool   `json:"died,omitempty"`
}

// Known is what the player has learned about a room. Exits map a direction to the room it
// led to, or "" while unexplored. Risk is the decision model's P(lethal) per direction,
// taken with the inventory listed in AssessedWith.
type Known struct {
	Name         string             `json:"name"`
	Kind         string             `json:"kind,omitempty"`
	Exits        map[string]string  `json:"exits"`
	Risk         map[string]float64 `json:"risk,omitempty"`
	AssessedWith []string           `json:"assessed_with,omitempty"`
	Assessed     bool               `json:"assessed"`
}

// State is the whole run. The engine is not stored: Commands is the replay log, and every
// node that needs the game rebuilds it. That keeps the checkpoint pure data and makes
// "reload after death" a slice operation.
type State struct {
	World        string              `json:"world"`
	Turn         int                 `json:"turn"`
	Commands     []world.Command     `json:"commands"`
	Obs          world.Observation   `json:"obs"`
	Map          map[string]*Known   `json:"map"`
	Fatal        map[string][]string `json:"fatal,omitempty"` // room -> commands that killed
	Tried        map[string][]string `json:"tried,omitempty"` // room -> commands that did nothing
	Deaths       int                 `json:"deaths"`
	LastProgress int                 `json:"last_progress"`
	Candidates   []string            `json:"candidates,omitempty"`
	Chosen       *Choice             `json:"chosen,omitempty"`
	Ledger       []Entry             `json:"ledger"`
	Won          bool                `json:"won"`
	Stopped      bool                `json:"stopped"`
	OutputPath   string              `json:"output_path,omitempty"`
}

func (s *State) here() *Known {
	if s.Map == nil {
		s.Map = make(map[string]*Known)
	}
	k, ok := s.Map[s.Obs.Room]
	if !ok {
		k = &Known{Name: s.Obs.Name, Exits: make(map[string]string, len(s.Obs.Exits))}
		for _, dir := range s.Obs.Exits {
			k.Exits[dir] = ""
		}
		s.Map[s.Obs.Room] = k
	}
	return k
}

// assessed reports whether the current room was assessed with the current inventory.
// A new item changes what is lethal, so it earns a fresh assessment.
func (s *State) assessed() bool {
	k := s.here()
	return k.Assessed && slices.Equal(k.AssessedWith, s.Obs.Inventory)
}

func (s *State) ruledOut(room, command string) bool {
	return slices.Contains(s.Fatal[room], command) || slices.Contains(s.Tried[room], command)
}

func (s *State) riddleAttempts() int {
	n := 0
	for _, c := range s.Tried[s.Obs.Room] {
		if cmd, err := world.Parse(c); err == nil && cmd.Verb == world.Answer {
			n++
		}
	}
	return n
}

func (s *State) record(e Entry) {
	s.Ledger = append(s.Ledger, e)
}

// Summary counts turns per layer, in layer order.
func (s *State) Summary() []LayerCount {
	counts := map[Layer]int{}
	for _, e := range s.Ledger {
		counts[e.Layer]++
	}
	out := make([]LayerCount, 0, 5)
	for _, l := range []Layer{LayerCode, LayerDecision, LayerSmall, LayerLarge, LayerHuman} {
		if counts[l] > 0 {
			out = append(out, LayerCount{Layer: l, Turns: counts[l]})
		}
	}
	return out
}

// LayerCount is one row of the summary.
type LayerCount struct {
	Layer Layer `json:"layer"`
	Turns int   `json:"turns"`
}

// Decision is what a human answers at the gate.
type Decision struct {
	Command string
	Stop    bool
}

// Screen is what the paused run shows to the human.
type Screen struct {
	Obs     world.Observation `json:"obs"`
	Turn    int               `json:"turn"`
	Deaths  int               `json:"deaths"`
	Why     string            `json:"why"`
	Recent  []Entry           `json:"recent"`
	Summary []LayerCount      `json:"summary"`
}
