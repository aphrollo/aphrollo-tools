package postedit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// The edit's run carries its lint. A Go run phase, once its tests have ended
// and while it still holds the run's governor slot, runs the commit stage's own
// linter command (suite.GoLintRunner) over the packages the edit touched, into a
// log of its own, and records only that it ran and how it exited. The hook that
// reports the run adds what the linter found to the run's line as guidance: a
// lint finding is never a deny and never a test verdict, so it changes neither
// the outcome token nor what counts as not tested, and it starts no job of its
// own.

// lintPhaseBudget is how long the run's lint may take before it is ended and
// reported as not run, so a cold linter cache never holds the slot for long.
const lintPhaseBudget = 3 * time.Minute

// phaseLintFn says which linter command a run phase also runs, false when it
// asks for none: not a Go run, not a run phase, no linter installed, nothing
// touched that lints. A seam, so a test states the linter without installing one.
var phaseLintFn = goPhaseLint

// goPhaseLint is the commit stage's lint command over the packages j's files
// are in.
func goPhaseLint(j DeferredJob) (Runner, bool) {
	if j.Phase != "run" || len(j.Runner) == 0 || j.Runner[0] != "go" || !lintEditLook() {
		return Runner{}, false
	}
	var rels []string
	for _, f := range append([]string{j.File}, j.Touched...) {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		if rel, err := filepath.Rel(j.Project, f); err == nil && !strings.HasPrefix(rel, "..") {
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	if len(rels) == 0 {
		return Runner{}, false
	}
	r := goLintRunner(j.Project, rels)
	return r, len(r.Args) > 2
}

// lintLogPath is where a run's lint output is written, beside the test log.
func lintLogPath(j DeferredJob) string { return j.Log + ".lint" }

// runPhaseLint runs j's lint, when it asks for one, inside the slot the caller
// holds. ran is false when it did not run or could not finish: another lint
// held the box-wide lint lock, the budget ran out, the log could not be opened.
func runPhaseLint(j DeferredJob) (ran bool, exit int) {
	r, ok := phaseLintFn(j)
	if !ok {
		return false, 0
	}
	release, got := TryAcquireLintLock(cmdString(r), j.Project)
	if !got {
		return false, 0
	}
	defer release()
	log, err := os.Create(lintLogPath(j))
	if err != nil {
		return false, 0
	}
	defer log.Close()
	spec := run.Spec{Name: r.Cmd, Args: r.Args, Dir: j.Dir, Env: suiteEnv(r, j.Project), Stdout: log, Stderr: log, Timeout: lintPhaseBudget}
	capped, err := RunSlotSpec(spec, j.Dir)
	if capped.Killed || errors.Is(err, run.ErrTimeout) {
		return false, 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return true, ee.ExitCode()
	}
	return err == nil, 0
}

// lintGuidance is what a finished run's lint adds to its line: the findings
// golangci-lint named, as guidance; "" when it was not run, found nothing, or
// could not judge (exit 3 is its own lock, not a finding). A "(typecheck)"
// line is the package failing to load, which is the build's finding and the
// test run's to report.
func lintGuidance(j DeferredJob, out PhaseOutcome) string {
	if !out.LintRan || out.LintExit != 1 {
		return ""
	}
	data, err := os.ReadFile(lintLogPath(j))
	if err != nil {
		return ""
	}
	var findings []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if lintEditFinding.MatchString(line) && !strings.HasSuffix(line, "(typecheck)") {
			findings = append(findings, line)
		}
	}
	if len(findings) == 0 {
		return ""
	}
	noun := "findings"
	if len(findings) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("lint guidance, not a test verdict: %d lint %s a commit would refuse: %s", len(findings), noun, namedFindings(findings))
}

// withLintGuidance puts note on the run's line, which stays one line.
func withLintGuidance(line, note string) string {
	if note == "" {
		return line
	}
	return line + " · " + note
}
