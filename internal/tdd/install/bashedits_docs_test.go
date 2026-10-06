package install

import (
	"strings"
	"testing"
)

// A Bash call that writes source files is an edit like any other, and the
// skill every session reads says so; the managed block and the builder leave
// the detail to it.
func TestBashWrites_AreTaughtAsEdits(t *testing.T) {
	t.Parallel()
	flat := strings.Join(strings.Fields(strings.ToLower(TDDSkill())), " ")
	for _, want := range []string{"a bash script is a fine multi-file edit", "it gets the edit gate"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the tdd skill does not say %q", want)
		}
	}
}

// The skill is read by every session, so it must not imply that the session
// delegates its edits: delegation belongs to /sdd.
func TestTDDSkill_DoesNotImplyDelegation(t *testing.T) {
	t.Parallel()
	lower := strings.ToLower(TDDSkill())
	for _, gone := range []string{"builder", "delegate", "subagent", "coordinator"} {
		if strings.Contains(lower, gone) {
			t.Errorf("the tdd skill mentions %q", gone)
		}
	}
}
