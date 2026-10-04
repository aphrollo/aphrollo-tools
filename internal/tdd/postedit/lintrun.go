package postedit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// The edit's run carries its lint. A Go run phase, once its tests have ended,
// its result and store verdict are written and its slot is released, runs the
// commit stage's own linter command (suite.goLintRunner) over the packages the
// edit touched, under the box-wide lint lock, into a log of its own, and records
// there only that it ran and how it exited. The hook that reports the run adds
// what the linter found to the run's line as guidance when it has finished by
// then, and says nothing about lint when it has not. A lint finding is never a
// deny and never a test verdict, so it changes neither the outcome token nor
// what counts as not tested, and it starts no job of its own.

// lintPhaseBudget is how long the run's lint may take before it is ended and
// backed off, so a cold linter cache never holds the next edit's lint up. A var
// so a test can shorten it.
var lintPhaseBudget = 3 * time.Minute

// phaseLintFn says which linter command a run phase also runs, false when it
// asks for none: not a Go run, not a run phase, no linter installed, nothing
// touched that lints. A seam, so a test states the linter without installing one.
var phaseLintFn = goPhaseLint

// goPhaseLint is the commit stage's lint command over the packages j's files
// are in, relative to the module the run's own `go test` runs in (j.Dir), which
// is where the linter must load them from.
func goPhaseLint(j DeferredJob) (Runner, bool) {
	if j.Phase != "run" || len(j.Runner) == 0 || j.Runner[0] != "go" || !lintEditLook() {
		return Runner{}, false
	}
	module := j.Dir
	if module == "" {
		module = j.Project
	}
	var rels []string
	for _, f := range append([]string{j.File}, j.Touched...) {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		if rel, err := filepath.Rel(module, f); err == nil && !strings.HasPrefix(rel, "..") {
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	if len(rels) == 0 {
		return Runner{}, false
	}
	r := goLintRunner(module, rels)
	return r, len(r.Args) > 2
}

// lintLogPath is where a run's lint output is written, beside the test log, and
// lintDonePath the record that the lint finished.
func lintLogPath(j DeferredJob) string  { return j.Log + ".lint" }
func lintDonePath(j DeferredJob) string { return j.Log + ".lint.json" }

// removeLintFiles removes what an earlier run of the job's log left of its lint,
// wherever the log is started afresh: a lint file never outlives the run it
// belongs to.
func removeLintFiles(j DeferredJob) {
	if j.Log == "" {
		return
	}
	_ = os.Remove(lintLogPath(j))
	_ = os.Remove(lintDonePath(j))
}

// lintDone is the record a finished lint leaves: that it ran, and its exit.
type lintDone struct {
	Exit int `json:"exit"`
}

// runPhaseLint runs j's lint, when it asks for one, with the same stand-downs
// the edit-time lint had: a loaded box, a recent timeout, another lint holding
// the lint lock. A run past its budget is ended and backs the next one off. The
// record is written last, so a lint that did not finish leaves none.
func runPhaseLint(j DeferredJob) {
	r, ok := phaseLintFn(j)
	if !ok || lintBoxLoaded() || lintBackedOff(j.Project) {
		return
	}
	release, got := TryAcquireLintLock(cmdString(r), j.Project)
	if !got {
		return
	}
	defer release()
	log, err := os.Create(lintLogPath(j))
	if err != nil {
		return
	}
	defer log.Close()
	dir := r.Dir
	if dir == "" {
		dir = j.Dir
	}
	spec := run.Spec{Name: r.Cmd, Args: r.Args, Dir: dir, Env: suiteEnv(r, j.Project), Stdout: log, Stderr: log, Timeout: lintPhaseBudget}
	capped, err := RunSlotSpec(spec, dir)
	if errors.Is(err, run.ErrTimeout) {
		markLintBackoff(j.Project)
		return
	}
	exit := 0
	var ee *exec.ExitError
	switch {
	case capped.Killed:
		return
	case errors.As(err, &ee):
		exit = ee.ExitCode()
	case err != nil:
		return
	}
	if data, err := json.Marshal(lintDone{Exit: exit}); err == nil {
		_ = writeFileAtomic(lintDonePath(j), data)
	}
}

// lintGuidance is what a finished run's lint adds to its line: the findings
// golangci-lint named, as guidance; "" when the lint has not finished, was not
// run, found nothing, or could not judge (exit 3 is its own lock, not a
// finding). A "(typecheck)" line is the package failing to load, which is the
// build's finding and the test run's to report.
func lintGuidance(j DeferredJob) string {
	data, err := os.ReadFile(lintDonePath(j))
	if err != nil {
		return ""
	}
	var done lintDone
	if json.Unmarshal(data, &done) != nil || done.Exit != 1 {
		return ""
	}
	logged, err := os.ReadFile(lintLogPath(j))
	if err != nil {
		return ""
	}
	var findings []string
	for line := range strings.SplitSeq(string(logged), "\n") {
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
