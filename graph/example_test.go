package graph_test

import (
	"context"
	"fmt"

	"github.com/mateusveloso/graph-issue-triage/graph"
)

type Order struct {
	Total    float64
	Approved bool
}

type Approval struct{ Yes bool }

func ExampleRunner() {
	price := func(_ context.Context, o *Order) error { o.Total = 120; return nil }
	approve := func(ctx context.Context, o *Order) error {
		a, err := graph.Interrupt[Approval](ctx, fmt.Sprintf("approve %.0f?", o.Total))
		if err != nil {
			return err
		}
		o.Approved = a.Yes
		return nil
	}

	g := graph.New[Order]().
		Node("price", price).
		Node("approve", approve).
		Entry("price").
		Edge("price", "approve").
		Edge("approve", graph.End)

	r, _ := graph.NewRunner(g, &graph.MemoryStore[Order]{})
	ctx := context.Background()

	cp, _ := r.Start(ctx, "order-1", Order{})
	fmt.Println("paused:", cp.Pause.Payload)

	cp, _ = r.Resume(ctx, "order-1", Approval{Yes: true})
	fmt.Println("done:", cp.Done, "approved:", cp.State.Approved)
	// Output:
	// paused: approve 120?
	// done: true approved: true
}
