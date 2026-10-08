// Package graph is a small control plane for pipelines that mix code and models.
//
// A Graph is a static shape: named nodes joined by fixed edges or by routes, which are
// plain functions that read the state and name the next node. A Runner executes the
// shape against a Store, checkpointing after every node, so a run can stop at any point
// and continue later from another process.
//
// Nodes know nothing about each other and the package knows nothing about models.
// A node that needs a human calls Interrupt; the run pauses with a payload, and resumes
// with a typed answer when someone has one.
//
//	g := graph.New[State]().
//		Node("fetch", fetch).
//		Node("ask", ask).
//		Node("save", save).
//		Entry("fetch").
//		Edge("fetch", "ask").
//		Route("ask", func(s *State) string { if s.OK { return "save" }; return "fetch" }, "fetch", "save").
//		Edge("save", graph.End)
//
//	r, _ := graph.NewRunner(g, graph.FileStore[State]{Dir: ".runs"})
//	cp, _ := r.Start(ctx, "thread-1", State{})   // pauses at "ask"
//	cp, _ = r.Resume(ctx, "thread-1", Answer{OK: true})
package graph
