package merge

import (
	"io"
	"os"
	"strings"
	"testing"
)

// Local CI is the merge gate with the opt-in taken away: a repo that declares
// no laws and no mutants-at-merge still has its suites run, because here the
// run is the only CI there is. These tests pin what is judged, what is
// recorded and when a stored verdict stands in for a run.

func TestLocalCI_RunsTheSuitesOfARepoThatDeclaresNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)

	var seen []gateRun
	v, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard)
	if err != nil {
		t.Fatalf("a green merged tree must pass local CI: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("local CI ran no suite for a repo that declares nothing; the GitHub gate would have run them")
	}
	if v.Reused {
		t.Error("a first run reported itself as a reused verdict")
	}
	if len(v.Tree) != 40 {
		t.Errorf("verdict tree = %q, want the merge result's 40-hex tree hash", v.Tree)
	}
}

func TestLocalCI_ARedMergedTreeFailsAndIsNotStoredAsGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)

	var seen []gateRun
	if _, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: false, Output: "merged tree is red"}), io.Discard); err == nil ||
		!strings.Contains(err.Error(), "merged tree is red") {
		t.Fatalf("a red merged tree must fail local CI carrying the suite's words, got: %v", err)
	}
	first := len(seen)

	if _, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("after a red the next run must judge again, not replay the red: %v", err)
	}
	if len(seen) == first {
		t.Fatal("the second run reused a verdict that was never green")
	}
}

func TestLocalCI_AGreenVerdictForTheSameTreeIsReusedNotRerun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)

	var seen []gateRun
	first, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ran := len(seen)

	second, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: false, Output: "must not run"}), io.Discard)
	if err != nil {
		t.Fatalf("the same tree is already proven green: %v", err)
	}
	if len(seen) != ran {
		t.Fatalf("reran %d suite(s) for a tree with a stored green verdict", len(seen)-ran)
	}
	if !second.Reused || second.Tree != first.Tree {
		t.Errorf("second verdict = %+v, want Reused for tree %s", second, first.Tree)
	}
}

func TestLocalCI_ANewTreeIsJudgedEvenAfterAGreenForAnotherTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)
	var seen []gateRun
	if _, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatal(err)
	}
	ran := len(seen)

	write(t, root, "crates/a/src/more.rs", "pub fn more() -> i32 { 9 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane moves on")

	v, err := LocalCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if v.Reused || len(seen) == ran {
		t.Fatalf("a changed merge result must be judged again (reused=%v, new suite runs=%d)", v.Reused, len(seen)-ran)
	}
}

func TestLocalCI_RecordsTheVerdictInTheGateLogKeyedByTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)
	v, err := LocalCI(root, recordRuns(new([]gateRun), SuiteResult{Passed: true}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(GateLogPath())
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	var line string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, " ci ") {
			line = l
		}
	}
	if !strings.Contains(line, "local-ci:"+v.Tree) || !strings.Contains(line, " green ") {
		t.Fatalf("gate.log has no green local-ci line for tree %s:\n%s", v.Tree, data)
	}
}

func TestReadCIMode_DefaultsToAutoAndRefusesAnUnknownMode(t *testing.T) {
	for _, c := range []struct {
		toml, want string
		bad        bool
	}{
		{"", "auto", false},
		{"[aphrollo]\nci = \"local\"\n", "local", false},
		{"[aphrollo]\nci = \"github\"\n", "github", false},
		{"[aphrollo]\nci = \"auto\"\n", "auto", false},
		{"[aphrollo]\nci = \"selfhosted\"\n", "", true},
	} {
		root := t.TempDir()
		if c.toml != "" {
			write(t, root, "aphrollo.toml", c.toml)
		}
		got, err := ReadCIMode(root)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ReadCIMode(%q) = %q, %v; want %q (bad=%v)", c.toml, got, err, c.want, c.bad)
		}
	}
}
