package graph

import (
	"context"
	"fmt"
)

// interruptError carries the payload of a pause from a node to the Runner.
type interruptError struct{ payload any }

func (e *interruptError) Error() string { return "graph: interrupted" }

type ctxKey struct{ name string }

var (
	resumeKey = ctxKey{"resume"}
	threadKey = ctxKey{"thread"}
)

// resumeBox hands the resume value to the first Interrupt that asks for it. A node that
// interrupts twice in one execution pauses on the second call, as it should.
type resumeBox struct {
	value any
	used  bool
}

// Interrupt pauses the run and surfaces payload to whoever started it. When the run is
// resumed, the node executes again from the top and Interrupt returns the resume value,
// converted to T. A resume value of the wrong type is an error, not a panic.
func Interrupt[T any](ctx context.Context, payload any) (T, error) {
	var zero T
	box, ok := ctx.Value(resumeKey).(*resumeBox)
	if !ok || box.used {
		return zero, &interruptError{payload: payload}
	}
	box.used = true
	value, ok := box.value.(T)
	if !ok {
		return zero, fmt.Errorf("graph: resume value is %T, node expects %T", box.value, zero)
	}
	return value, nil
}

// Thread returns the id of the run executing the current node.
func Thread(ctx context.Context) string {
	id, _ := ctx.Value(threadKey).(string)
	return id
}
