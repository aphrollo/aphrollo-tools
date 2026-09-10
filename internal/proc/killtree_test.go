package proc

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestKillTreePlan_EndsTheWholeTree pins the leak this package exists for:
// killing only the direct pid left its cargo/rustc children compiling while
// the parent's death handed the target lock and the global slot to the next
// build. The kill must reach the process TREE — the group on unix, /T on
// Windows — not one pid.
func TestKillTreePlan_EndsTheWholeTree(t *testing.T) {
	plan := killTreePlan(4242)
	joined := strings.Join(plan, " ")
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(joined, "/T") {
			t.Fatalf("kill plan %q does not walk the tree (/T)", joined)
		}
	default:
		if !strings.Contains(joined, "-"+strconv.Itoa(4242)) {
			t.Fatalf("kill plan %q does not name the process GROUP (-pid)", joined)
		}
	}
	if !strings.Contains(joined, "4242") {
		t.Fatalf("kill plan %q does not name the pid", joined)
	}
}
