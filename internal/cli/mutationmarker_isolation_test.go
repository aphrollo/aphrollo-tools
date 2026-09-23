package cli

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestSuite_MutationGateMarkerDoesNotReachTheShimTests runs one shim queuing
// test in a child copy of this test binary with the mutation-gate marker set,
// the way a measurement's `go test -cover ./...` inherits it from the gate.
// The marker tells the cargo shim its caller already holds the box-wide
// mutation lock, so every shim test that inherited it took the marked path:
// queue lines never printed, refusals never fired, and
// TestRunCargoShim_RecordsQueueWaiterWhileWaitingThenRemoves waited forever,
// so the coverage gather died at Go's 10-minute default and no Go mutation
// measurement of this repo ever completed (#704).
func TestSuite_MutationGateMarkerDoesNotReachTheShimTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunCargoShim_WaitsPrintsQueuedOnceAndAcquiredOnce$", "-test.count=1")
	cmd.Env = append(os.Environ(), tdd.MutationGateEnv+"="+tdd.MutationGateMarked)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("with %s=%s inherited, the shim queuing test failed: %v\n%s", tdd.MutationGateEnv, tdd.MutationGateMarked, err, out)
	}
}
