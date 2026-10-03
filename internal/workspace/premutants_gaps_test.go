package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Issue #910 before a PR opens: a mutant no test runs, on a line the lane
// adds, stops the PR by name in the accept-list's own form, the same way
// the merge gate and CI refuse it.

// goLane is a Go module on main, pushed, then a lane that adds a function
// no test calls, also pushed. Line 4 of over.go is its comparison.
func goLane(t *testing.T) string {
	t.Helper()
	repo := repoWithRemote(t)
	writeRel(t, repo, "go.mod", "module example.com/m\n\ngo 1.26\n")
	writeRel(t, repo, "aphrollo.toml", "[aphrollo]\nmutants-at-merge = true\nmutants-before-pr = true\nmutants-at-merge-level = \"block\"\n")
	writeRel(t, repo, "m.go", "package m\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "trunk")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "lane")
	writeRel(t, repo, "over.go", "package m\n\nfunc Over(n int) bool {\n\treturn n > 4\n}\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "lane")
	gitIn(t, repo, "push", "-q", "-u", "origin", "lane")
	return repo
}

// stubGremlins stands in for gremlins: it writes report where the real tool
// writes its --output.
func stubGremlins(t *testing.T, report string) {
	t.Helper()
	t.Cleanup(tdd.SetMutantsGOOSForTest("linux"))
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, argv []string, _ io.Writer) (int, error) {
		i := slices.Index(argv, "--output")
		if i < 0 || i+1 >= len(argv) {
			t.Errorf("argv carries no --output: %v", argv)
			return 1, nil
		}
		if err := os.MkdirAll(filepath.Dir(argv[i+1]), 0o755); err != nil {
			return 1, err
		}
		return 0, os.WriteFile(argv[i+1], []byte(report), 0o600)
	}))
}

func TestPR_ANotCoveredMutantOnALineTheLaneAddsStopsThePRNamingIt(t *testing.T) {
	isolateMeasurement(t)
	repo := goLane(t)
	stubGremlins(t, `{"files":[{"file_name":"over.go","mutations":[`+
		`{"type":"CONDITIONALS_BOUNDARY","status":"NOT COVERED","line":4,"column":11}]}]}`)
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)

	if err == nil || *created {
		t.Fatalf("the PR opened over a gap the lane itself added (err %v)\nstdout: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "\nover.go:4:11 CONDITIONALS_BOUNDARY (not covered)\n") {
		t.Errorf("the gap is not named as file:line:col MUTATOR (not covered):\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--skip-mutants \"<reason>\" opens the PR unmeasured") {
		t.Errorf("the refusal does not offer the way to open the PR unmeasured:\n%s", out.String())
	}
}
