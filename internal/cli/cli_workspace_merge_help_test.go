package cli

import (
	"strings"
	"testing"
)

// A merge whose GitHub checks all skipped is judged by the local suite alone;
// `workspace merge --help` says so under --ci, so the refusal that follows a
// broken local suite is not a surprise.
func TestWorkspaceMergeHelp_SaysTheLocalSuiteIsTheProofWhenEveryCheckSkipped(t *testing.T) {
	var out, errb strings.Builder
	Run([]string{"workspace", "merge", "--help"}, strings.NewReader(""), &out, &errb)
	if got := out.String() + errb.String(); !strings.Contains(got, "all skipped has no CI verdict, so the local suite is the proof") {
		t.Errorf("help does not say the local suite is the proof:\n%s", got)
	}
}
