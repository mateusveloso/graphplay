package player

import (
	"github.com/mateusveloso/graphplay/graph"
	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/world"
)

// Build wires the player. Every dotted edge in the diagram is one of the route* functions
// below: a Go function reading typed state and a threshold from Config. Nodes are ordered
// by what they cost; the routes make sure the expensive ones only run when the cheap ones
// had nothing to say.
func Build(w world.World, models llm.Models, decider jev.Decider, cfg Config) *graph.Graph[State] {
	return graph.New[State]().
		Node("observe", observe(w)).
		Node("assess", assess(decider)).
		Node("cheap_move", cheapMove(cfg)).
		Node("propose", propose(models.Small)).
		Node("rank", rank(decider)).
		Node("solve", solve(models.Large)).
		Node("act", act(w)).
		Node("gate", gate).
		Node("finalize", finalize(cfg)).
		Entry("observe").
		Route("observe", routeAfterObserve(cfg), "finalize", "gate", "solve", "assess", "cheap_move").
		Edge("assess", "cheap_move").
		Route("cheap_move", routeOnChoice("propose"), "act", "propose").
		Edge("propose", "rank").
		Route("rank", routeOnChoice("gate"), "act", "gate").
		Edge("solve", "act").
		Edge("act", "observe").
		Route("gate", routeAfterGate, "act", "finalize").
		Edge("finalize", graph.End)
}

// routeAfterObserve is the turn's triage, in priority order: the game is over; a human is
// owed a look; a riddle is posed; the room is new; or code gets to move.
func routeAfterObserve(cfg Config) graph.Router[State] {
	return func(s *State) string {
		switch {
		case s.Won || s.Stopped || s.Turn >= cfg.MaxTurns:
			return "finalize"
		case s.Deaths >= cfg.MaxDeaths, s.Turn-s.LastProgress >= cfg.StuckTurns:
			return "gate"
		case s.Obs.Riddle != "" && s.riddleAttempts() >= cfg.MaxRiddleAttempts:
			return "gate"
		case s.Obs.Riddle != "":
			return "solve"
		case !s.assessed():
			return "assess"
		default:
			return "cheap_move"
		}
	}
}

// routeOnChoice goes to act when the node picked a command, else to the fallback.
func routeOnChoice(fallback string) graph.Router[State] {
	return func(s *State) string {
		if s.Chosen != nil {
			return "act"
		}
		return fallback
	}
}

func routeAfterGate(s *State) string {
	if s.Stopped {
		return "finalize"
	}
	return "act"
}
