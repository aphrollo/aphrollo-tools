//go:build !windows

package ghworkflow

import (
	"reflect"
	"strings"
	"testing"
)

func TestLowPriorityArgv_TheCommandRunsLastAndIntactUnderNice(t *testing.T) {
	cmd := []string{"/bin/bash", "-c", "make build"}
	got := lowPriorityArgv(cmd)
	if len(got) < len(cmd)+3 || !reflect.DeepEqual(got[len(got)-len(cmd):], cmd) {
		t.Fatalf("lowPriorityArgv = %q, want the command last and whole, behind its wrappers", got)
	}
	wrapped := strings.Join(got[:len(got)-len(cmd)], " ")
	if !strings.HasSuffix(wrapped, "/nice -n 10") {
		t.Errorf("the wrappers = %q, want nice -n 10 directly before the command", wrapped)
	}
	if len(cmd) != 3 || cmd[0] != "/bin/bash" {
		t.Errorf("the caller's argv was changed: %q", cmd)
	}
}

func TestPriorityNote_NamesWhatTheStepsRunUnder(t *testing.T) {
	if note := priorityNote(); !strings.Contains(note, "nice -n 10") {
		t.Errorf("priorityNote = %q, want it to name nice -n 10", note)
	}
}
