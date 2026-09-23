package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSmell_TestSleep_ADeadlineArmReadsItsSelectFromTheWholeFile pins the
// mechanism. The edit-time gate judges only the lines an edit ADDS, so an edit
// that appends the timer arm to a select that already exists offers the policy
// one line and no surrounding block. The verdict still has to come out right,
// which means the policy must read its context from the file's full
// post-image, not from the added slice.
func TestSmell_TestSleep_ADeadlineArmReadsItsSelectFromTheWholeFile(t *testing.T) {
	t.Parallel()
	before := strings.Replace(watchdogSelectTest,
		"\tcase <-time.After(30 * time.Second):\n\t\tt.Fatal(\"a mount cycle never terminated\")\n", "", 1)
	path := filepath.Join(t.TempDir(), "watchdog_test.go")
	mustWrite(t, path, before)

	raw := ratchetPayload(t, "Edit", path, map[string]any{
		"old_string": "\t\t}\n\t}\n}\n",
		"new_string": "\t\t}\n\tcase <-time.After(30 * time.Second):\n\t\tt.Fatal(\"a mount cycle never terminated\")\n\t}\n}\n",
	})

	if d := decide(t, string(raw)); d.Action == Block {
		t.Fatalf("adding a deadline arm to an existing select must flow; the enclosing select is in the file even when it is not in the diff: %s", d.Reason)
	}
}
