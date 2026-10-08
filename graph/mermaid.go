package graph

import (
	"fmt"
	"slices"
	"strings"
)

// Mermaid draws the graph as a flowchart. Solid edges are fixed; dotted edges are routes.
// The output is deterministic so it can be committed and diffed.
func (g *Graph[S]) Mermaid() string {
	var b strings.Builder
	b.WriteString("graph TD;\n")
	b.WriteString("\t__start__([start]):::first\n")
	for _, name := range g.order {
		fmt.Fprintf(&b, "\t%s(%s)\n", name, name)
	}
	b.WriteString("\t__end__([end]):::last\n")
	fmt.Fprintf(&b, "\t__start__ --> %s;\n", g.entry)
	for _, from := range g.order {
		if to, ok := g.edges[from]; ok {
			fmt.Fprintf(&b, "\t%s --> %s;\n", from, to)
		}
		if rt, ok := g.routes[from]; ok {
			for _, to := range slices.Sorted(slices.Values(rt.targets)) {
				fmt.Fprintf(&b, "\t%s -.-> %s;\n", from, to)
			}
		}
	}
	b.WriteString("\tclassDef default fill:#f2f0ff,line-height:1.2\n")
	b.WriteString("\tclassDef first fill-opacity:0\n")
	b.WriteString("\tclassDef last fill:#bfb6fc\n")
	return b.String()
}
