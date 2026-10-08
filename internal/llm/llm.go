// Package llm is the only place a generative model is called from.
package llm

import "context"

// Model answers a prompt with a typed object. That is the whole contract a node gets:
// no free text, no tool loop, no conversation.
type Model interface {
	Ask(ctx context.Context, system, user string, out any) error
}

// Models is one model per role. Roles, not nodes: the graph decides who calls what.
type Models struct {
	Planner Model
	Writer  Model
	Critic  Model
}

// Single uses the same model for every role. Handy for tests.
func Single(m Model) Models {
	return Models{Planner: m, Writer: m, Critic: m}
}
