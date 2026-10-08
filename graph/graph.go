package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// End is the terminal target of an edge or a route.
const End = "__end__"

// Node mutates the state in place. A returned error aborts the run; a pending Interrupt
// pauses it.
type Node[S any] func(ctx context.Context, s *S) error

// Router reads the state and names the next node. It must return one of the targets
// declared on the Route; anything else is a programming error the Runner reports.
type Router[S any] func(s *S) string

type route[S any] struct {
	pick    Router[S]
	targets []string
}

// Graph is the static shape of a pipeline. Build it once; run it many times.
type Graph[S any] struct {
	entry  string
	order  []string // insertion order, for stable diagrams
	nodes  map[string]Node[S]
	edges  map[string]string
	routes map[string]route[S]
}

// New returns an empty graph over state type S.
func New[S any]() *Graph[S] {
	return &Graph[S]{
		nodes:  make(map[string]Node[S]),
		edges:  make(map[string]string),
		routes: make(map[string]route[S]),
	}
}

// Node registers fn under name. Registering a name twice is a programming error.
func (g *Graph[S]) Node(name string, fn Node[S]) *Graph[S] {
	if _, dup := g.nodes[name]; dup {
		panic(fmt.Sprintf("graph: duplicate node %q", name))
	}
	g.nodes[name] = fn
	g.order = append(g.order, name)
	return g
}

// Entry names the node a new run starts from.
func (g *Graph[S]) Entry(name string) *Graph[S] {
	g.entry = name
	return g
}

// Edge is a fixed transition: after from, always to.
func (g *Graph[S]) Edge(from, to string) *Graph[S] {
	g.edges[from] = to
	return g
}

// Route is a decision: after from, pick chooses one of targets by reading the state.
// Targets are declared so the diagram can draw them and the Runner can reject a router
// that wanders off.
func (g *Graph[S]) Route(from string, pick Router[S], targets ...string) *Graph[S] {
	g.routes[from] = route[S]{pick: pick, targets: targets}
	return g
}

// Validate reports every structural problem at once: a missing entry, a node with no
// outgoing edge or with both kinds, and edges that point nowhere.
func (g *Graph[S]) Validate() error {
	var errs []error
	if _, ok := g.nodes[g.entry]; !ok {
		errs = append(errs, fmt.Errorf("entry %q is not a node", g.entry))
	}
	known := func(from, to string) error {
		if to == End {
			return nil
		}
		if _, ok := g.nodes[to]; !ok {
			return fmt.Errorf("%q points to unknown node %q", from, to)
		}
		return nil
	}
	for _, name := range g.order {
		_, fixed := g.edges[name]
		rt, routed := g.routes[name]
		switch {
		case fixed && routed:
			errs = append(errs, fmt.Errorf("node %q has both an Edge and a Route", name))
		case !fixed && !routed:
			errs = append(errs, fmt.Errorf("node %q has no outgoing edge", name))
		case fixed:
			errs = append(errs, known(name, g.edges[name]))
		case routed:
			for _, to := range rt.targets {
				errs = append(errs, known(name, to))
			}
		}
	}
	return errors.Join(errs...)
}

// next resolves the transition out of from for the current state.
func (g *Graph[S]) next(from string, s *S) (string, error) {
	if to, ok := g.edges[from]; ok {
		return to, nil
	}
	rt, ok := g.routes[from]
	if !ok {
		return "", fmt.Errorf("graph: node %q has no outgoing edge", from)
	}
	to := rt.pick(s)
	if !slices.Contains(rt.targets, to) {
		return "", fmt.Errorf("graph: route out of %q returned undeclared target %q", from, to)
	}
	return to, nil
}
