package workspace

import (
	"os"
	"strings"
	"testing"
	"time"
)

// gitRemoteBranchExists must not silently answer "false" (the shape it
// answers for an ordinary "no such ref"/"no remote") when it could not
// complete the check at all — that collapse would make BuildPlan create a
// brand-new local branch off the default start point while an
// identically-named branch already sits on origin, diverging from it. Found
// against issue #351's stated risk (the earlier #290 fix once mapped a `gh`
// timeout onto "no PR found", a wrong answer of the same shape).
func TestGitRemoteBranchExists_ReturnsAnErrorRatherThanFalseOnATimeout(t *testing.T) {
	putSlowStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { gitNetworkTimeout = d }(gitNetworkTimeout)
	gitNetworkTimeout = 200 * time.Millisecond

	started := time.Now()
	exists, err := gitRemoteBranchExists(t.TempDir(), "feat/x")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatalf("gitRemoteBranchExists = (%v, nil) on a stalled ls-remote — a timeout must be an error, not a silent false", exists)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error %q does not name the timeout", err)
	}
	if exists {
		t.Fatalf("gitRemoteBranchExists reported exists=true alongside an error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("gitRemoteBranchExists waited %s — the deadline did not fire", elapsed)
	}
}

// A Network step must give up at gitNetworkTimeout instead of hanging for
// however long the subprocess takes — the gap #351 named: every OTHER
// network call in this package already bounds itself, but workspace
// create/prepare's own `fetch origin` step ran through Apply's bare
// exec.Command with no deadline at all. Exercises runStep directly (not the
// whole Apply loop) because Apply's post-loop reportBase also runs `git` and
// would inherit the same PATH-stubbed sleep, making the deadline
// unobservable through that seam.
func TestRunStep_BoundsANetworkStepToTheDeadlineInsteadOfHangingForever(t *testing.T) {
	putSlowStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { gitNetworkTimeout = d }(gitNetworkTimeout)
	gitNetworkTimeout = 200 * time.Millisecond

	step := Step{Title: "fetch origin", Cmd: []string{"git", "fetch", "origin", "--quiet"}, Network: true}
	var out, errb strings.Builder
	started := time.Now()
	// A real Apply() call always passes a real environment (os.Environ()
	// plus CI=1); this test does the same rather than nil, which the
	// Network branch's own env-append would otherwise turn into a
	// ONE-ELEMENT slice (dropping SLOWSTUB_SLEEP_MS along with everything
	// else Go would have inherited for a genuinely nil Env), silently
	// defeating the stub instead of exercising the deadline at all.
	err := runStep(step, os.Environ(), &out, &errb)
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("runStep = %v, want a timeout error", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("runStep waited %s for a Network step — the deadline did not fire", elapsed)
	}
}
