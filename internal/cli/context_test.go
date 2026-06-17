package cli

import (
	"testing"
	"time"
)

// Every refactor command must run under a bounded, cancellable context so a
// hung-but-alive language server cannot wedge the CLI forever. commandContext
// must carry a deadline and propagate its cancel.
func TestCommandContext_BoundedAndCancellable(t *testing.T) {
	ctx, cancel := commandContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("commandContext: want a deadline, got none")
	}
	if d := time.Until(deadline); d <= 0 || d > commandTimeout+time.Second {
		t.Fatalf("deadline %v out of expected range (0, %v]", d, commandTimeout)
	}

	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("commandContext: context not done after cancel()")
	}
}
