package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestIssue_TopLevelReachesTheSameFunctionAsGateIssue proves the top-level
// spelling is the exact same code path as the old one — not a second
// implementation that could drift — by recording the gh argv each produces
// through the same stub and comparing them byte for byte.
func TestIssue_TopLevelReachesTheSameFunctionAsGateIssue(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer
	code := Run([]string{"issue", "the rig drifts at 60 Hz", "--label", "physics", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	topArgv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	repo2, log2 := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out2, errb2 bytes.Buffer
	code2 := Run([]string{"gate", "issue", "the rig drifts at 60 Hz", "--label", "physics", "--repo", repo2},
		strings.NewReader(""), &out2, &errb2)
	if code2 != 0 {
		t.Fatalf("gate issue exit = %d, want 0\nstderr: %s", code2, errb2.String())
	}
	gateArgv, err := os.ReadFile(log2)
	if err != nil {
		t.Fatal(err)
	}

	if string(topArgv) != string(gateArgv) {
		t.Errorf("argv diverged:\n  issue:      %s\n  gate issue: %s", topArgv, gateArgv)
	}
	if out.String() != out2.String() {
		t.Errorf("stdout diverged: %q vs %q", out.String(), out2.String())
	}
}
