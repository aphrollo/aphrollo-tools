package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestTimeoutAdvisory_PointsAtGateStatusNotARerun pins issue #430's fix: a
// TIMEOUT line must still say the run was inconclusive, but it must also
// name `gate status` and warn that a rerun queues behind the same holder,
// rather than leaving silence where a rerun used to be the only visible move.
func TestTimeoutAdvisory_PointsAtGateStatusNotARerun(t *testing.T) {
	t.Parallel()
	got := timeoutAdvisory(Runner{Cmd: "cargo", Args: []string{"test"}}, "/repo/a", 42*time.Second)
	if !strings.Contains(got, "TIMEOUT after 42s") || !strings.Contains(got, "inconclusive") {
		t.Fatalf("advisory lost its core claim: %q", got)
	}
	if !strings.Contains(got, "aphrollo gate status") {
		t.Errorf("advisory does not point at gate status: %q", got)
	}
	if !strings.Contains(got, "queues behind") {
		t.Errorf("advisory does not warn that a rerun queues behind the holder: %q", got)
	}
}

// TestQueuedSkippedAdvisory_PointsAtGateStatusToo pins the same fix for the
// QUEUED-SKIPPED line: it already names the one holder that blocked THIS
// edit; it must now also point at `gate status` for the box's full slot
// table, and carry the same "a rerun queues too" caution.
func TestQueuedSkippedAdvisory_PointsAtGateStatusToo(t *testing.T) {
	t.Parallel()
	got := queuedSkippedAdvisory("/repo/a", "/repo/a/target")
	if !strings.Contains(got, "QUEUED-SKIPPED") || !strings.Contains(got, "inconclusive") {
		t.Fatalf("advisory lost its core claim: %q", got)
	}
	if !strings.Contains(got, "aphrollo gate status") {
		t.Errorf("advisory does not point at gate status: %q", got)
	}
	if !strings.Contains(got, "queues behind") {
		t.Errorf("advisory does not warn that a rerun queues behind it too: %q", got)
	}
}
