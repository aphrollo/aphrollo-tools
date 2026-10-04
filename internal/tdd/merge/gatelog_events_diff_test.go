package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The merge gate's three readers moved from gate.log to the v1 events. Each
// answer here is checked against the old reading, kept as an oracle that parses
// the lines gate.log would have held for the same history.

type histRow struct{ stage, root, cmd, verdict string }

func oracleLines(rows []histRow) []string {
	var out []string
	for i, r := range rows {
		at := time.Date(2026, 9, 2, 10, 0, i, 0, time.UTC).Format(time.RFC3339)
		out = append(out, fmt.Sprintf("%s %s %s %s %s 1.0s", at, r.stage, r.root, r.cmd, r.verdict))
	}
	return out
}

func oracleLastPrecommit(rows []histRow, root string) string {
	last := ""
	for _, l := range oracleLines(rows) {
		e, ok := parseGateLine(l)
		if !ok || e.Stage != "precommit" || e.Verdict == "ran" || !sameProject(e.Root, root) {
			continue
		}
		last = e.Verdict
	}
	return last
}

func oracleStoredGreen(rows []histRow, root, tree string) bool {
	for _, l := range oracleLines(rows) {
		if e, ok := parseGateLine(l); ok && sameProject(e.Root, root) && e.Stage == ciStage && e.Cmd == "local-ci:"+tree && e.Verdict == "green" {
			return true
		}
	}
	return false
}

func TestGateEvents_ReadersAnswerAsTheGateLogReadersDid(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repoA := gitRepo(t)
	repoB := gitRepo(t)
	rows := []histRow{
		{"precommit", repoA, "cargo-test", "green"},
		{"precommit", repoB, "cargo-test", "timeout"},
		{"precommit", repoA, "cargo-test", "ran"},
		{"ci", repoA, "local-ci:treeone", "red"},
		{"ci", repoA, "local-ci:treetwo", "green"},
		{"precommit", repoA, "cargo-test", "cache-hit"},
		{"ci", repoB, "local-ci:treethree", "green"},
		{"postedit", repoA, "go-test", "green"},
	}
	for _, r := range rows {
		AppendGateLog(r.stage, r.root, r.cmd, r.verdict, time.Second)
	}
	for _, root := range []string{repoA, repoB, t.TempDir()} {
		if got, want := lastPrecommitVerdict(root), oracleLastPrecommit(rows, root); got != want {
			t.Errorf("lastPrecommitVerdict(%s) = %q, the gate.log reading gave %q", root, got, want)
		}
	}
	for _, tree := range []string{"treeone", "treetwo", "treethree", "unknown", ""} {
		if got, want := storedGreen(repoA, tree), oracleStoredGreen(rows, repoA, tree); got != want {
			t.Errorf("storedGreen(%q) = %v, the gate.log reading gave %v", tree, got, want)
		}
	}
	if storedGreen(repoA, "treethree") {
		t.Error("a green recorded in another repository must not answer for this one")
	}
	if !storedGreen(repoB, "treethree") {
		t.Error("a repository's own green must answer for it")
	}
	if !strings.Contains(strings.Join(oracleLines(rows), "\n"), "local-ci:treetwo green") {
		t.Fatal("setup: the oracle history lost its green line")
	}
}

// A box upgraded from a release that wrote gate.log has history there and none
// in the events. Both the last commit-gate verdict and a local-CI green for a
// tree still read from it, until the fallback is removed (2026-11-05); a green
// is keyed by the tree, so reusing it is as safe as it was.
func TestGateEvents_AColdStartReadsGateLogHistory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := gitRepo(t)
	lines := oracleLines([]histRow{
		{"precommit", repo, "cargo-test", "green"},
		{"ci", repo, "local-ci:oldtree", "green"},
		{"precommit", repo, "cargo-test", "cache-hit"},
	})
	if err := os.MkdirAll(filepath.Dir(GateLogPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GateLogPath(), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastPrecommitVerdict(repo); got != "cache-hit" {
		t.Errorf("lastPrecommitVerdict = %q, want the pre-upgrade verdict cache-hit", got)
	}
	if !storedGreen(repo, "oldtree") {
		t.Error("a pre-upgrade local-CI green for the same tree must still be reused")
	}
}
