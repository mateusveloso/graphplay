package triage

import (
	"github.com/mateusveloso/graph-issue-triage/graph"
	"github.com/mateusveloso/graph-issue-triage/internal/github"
	"github.com/mateusveloso/graph-issue-triage/internal/jev"
	"github.com/mateusveloso/graph-issue-triage/internal/llm"
)

// Build wires the nodes. Every dotted edge in the diagram is one of the route* functions
// below: a Go function reading typed state and a threshold from Config.
func Build(models llm.Models, repo github.Repository, decider jev.Decider, cfg Config) *graph.Graph[State] {
	return graph.New[State]().
		Node("load_issue", loadIssue(repo)).
		Node("classify", classify(decider)).
		Node("planner", plan(models.Planner)).
		Node("collect", collect(repo, cfg)).
		Node("writer", write(models.Writer)).
		Node("check", check(decider, cfg)).
		Node("critic", critic(models.Critic)).
		Node("gate", gate).
		Node("finalize", finalize(cfg)).
		Entry("load_issue").
		Edge("load_issue", "classify").
		Route("classify", routeAfterClassify(cfg), "planner", "writer").
		Edge("planner", "collect").
		Edge("collect", "writer").
		Edge("writer", "check").
		Route("check", routeAfterCheck(cfg), "writer", "critic", "gate").
		Route("critic", routeAfterCritic(cfg), "writer", "gate").
		Route("gate", routeAfterGate, "writer", "finalize").
		Edge("finalize", graph.End)
}

// routeAfterClassify skips reading code only when the decision model is confident it is
// not needed.
func routeAfterClassify(cfg Config) graph.Router[State] {
	return func(s *State) string {
		if s.Prior.NeedsCode != nil && *s.Prior.NeedsCode <= cfg.SkipCodeBelow {
			return "writer"
		}
		return "planner"
	}
}

// routeAfterCheck: reject -> writer while the budget lasts, else gate; pass -> gate;
// uncertain -> critic.
func routeAfterCheck(cfg Config) graph.Router[State] {
	return func(s *State) string {
		switch {
		case s.Verdict == VerdictUncertain:
			return "critic"
		case s.Verdict == VerdictPass, s.CriticRounds >= cfg.MaxCriticRounds:
			return "gate"
		default:
			return "writer"
		}
	}
}

func routeAfterCritic(cfg Config) graph.Router[State] {
	return func(s *State) string {
		last := s.Reviews[len(s.Reviews)-1]
		if last.Approved || s.CriticRounds >= cfg.MaxCriticRounds {
			return "gate"
		}
		return "writer"
	}
}

func routeAfterGate(s *State) string {
	if s.Approved {
		return "finalize"
	}
	return "writer"
}
