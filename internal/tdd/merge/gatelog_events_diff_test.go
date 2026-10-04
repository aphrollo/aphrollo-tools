package merge

import (
	"fmt"
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

func oracleStoredGreen(rows []histRow, tree string) bool {
	for _, l := range oracleLines(rows) {
		if e, ok := parseGateLine(l); ok && e.Stage == ciStage && e.Cmd == "local-ci:"+tree && e.Verdict == "green" {
			return true
		}
	}
	return false
}

func TestGateEvents_ReadersAnswerAsTheGateLogReadersDid(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repoA := t.TempDir()
	repoB := t.TempDir()
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
		if got, want := storedGreen(repoA, tree), oracleStoredGreen(rows, tree); got != want {
			t.Errorf("storedGreen(%q) = %v, the gate.log reading gave %v", tree, got, want)
		}
	}
	if !strings.Contains(strings.Join(oracleLines(rows), "\n"), "local-ci:treetwo green") {
		t.Fatal("setup: the oracle history lost its green line")
	}
}
