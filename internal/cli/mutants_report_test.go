package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `--report` is how the box that CAN measure hands its measurement to the box
// that cannot: gremlins reads 0.00% mutator coverage on Windows, so the Go
// half is measured on the self-hosted Linux runner and consumed here (issue
// #697). What travels has to be bound to the tree it was measured on, or the
// consuming gate is trusting a measurement of different code — so the verb is
// judged on exactly that: the report it writes names the tree the run
// measured, and carries the outcomes rather than a verdict.
func TestGateMutantsRun_ReportPublishesTheOutcomesBoundToTheTreeMeasured(t *testing.T) {
	gateConfigDir(t)
	t.Cleanup(tdd.SetFreeSpaceForTest(999, true))
	// The runner this report exists for is Linux; on Windows the Go half
	// consumes a report rather than producing one.
	t.Cleanup(tdd.SetMutantsGOOSForTest("linux"))
	root, base := goLaneRepo(t)
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, argv []string, _ io.Writer) (int, error) {
		writeIn(t, filepath.Dir(gremlinsOutputPath(t, argv)), filepath.Base(gremlinsOutputPath(t, argv)),
			`{"files":[{"file_name":"calc.go","mutations":[
				{"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":20}]}]}`)
		return 0, nil
	}))
	inDir(t, root)
	out := filepath.Join(t.TempDir(), "mutants-verdict.json")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--base", base, "--report", out},
		strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a run whose one mutant was caught\nstdout: %s\nstderr: %s",
			code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no report written to --report: %v\nstderr: %s", err, stderr.String())
	}
	var report struct {
		Tree    string `json:"tree"`
		Base    string `json:"base"`
		Mutants []struct {
			File   string `json:"file"`
			Status string `json:"status"`
		} `json:"mutants"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("the report is not readable JSON: %v\n%s", err, data)
	}
	if want := strings.TrimSpace(gitOutIn(t, root, "write-tree")); report.Tree != want {
		t.Errorf("tree = %q, want %q — the tree this run actually measured", report.Tree, want)
	}
	if report.Base != base {
		t.Errorf("base = %q, want the base the diff was scoped against (%q)", report.Base, base)
	}
	if len(report.Mutants) != 1 || report.Mutants[0].File != "calc.go" || report.Mutants[0].Status != "caught" {
		t.Errorf("mutants = %+v, want the run's own per-mutant outcomes, judged by whoever consumes them",
			report.Mutants)
	}
}

// goLaneRepo builds a one-file Go module on `main`, then a lane branch with
// one changed source on top of it.
func goLaneRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.email", "t@t")
	gitIn(t, root, "config", "user.name", "t")
	writeIn(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	writeIn(t, root, "calc.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "trunk")
	base = strings.TrimSpace(gitOutIn(t, root, "rev-parse", "HEAD"))
	gitIn(t, root, "checkout", "-q", "-b", "lane")
	writeIn(t, root, "calc.go", "package m\n\nfunc Add(a, b int) int { return a + b + 0 }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "lane")
	return root, base
}

// gremlinsOutputPath is where the run under test was told to write its
// machine-readable report, read off the argv it was invoked with.
func gremlinsOutputPath(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "--output" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("no --output in the runner's argv: %v", argv)
	return ""
}
