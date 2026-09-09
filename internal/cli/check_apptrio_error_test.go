package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

// appProfileRepo is a checkout whose basename is the one entry in the app
// table, so HasAppProfile says yes and the trio guard is past its skip.
func appProfileRepo(t *testing.T) string {
	t.Helper()
	parent := resolvedTempDir(t)
	root := filepath.Join(parent, "aphrollo-web")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Once HasAppProfile has said the repo DOES declare an app, a failure to
// resolve that app is a broken checkout, not an absent declaration. Printing
// "[skip] no app declared" states the opposite of the fact established one
// line above and skips the trio on a repo that is gated by it.
func TestCheckAppTrio_ResolveFailureIsAMiss_NotASkip(t *testing.T) {
	root := appProfileRepo(t)
	original := checkAppTrioResolve
	checkAppTrioResolve = func(string) (*workspace.Target, error) {
		return nil, fmt.Errorf("worktree parent unreadable")
	}
	t.Cleanup(func() { checkAppTrioResolve = original })

	var out, errb bytes.Buffer
	if checkAppTrio(root, &out, &errb) {
		t.Fatalf("an unresolvable declared app must fail the guard; stdout:\n%s", out.String())
	}
	got := out.String()
	if strings.Contains(got, "[skip] no app declared") {
		t.Fatalf("the repo DOES declare an app; the guard must not claim otherwise; got:\n%s", got)
	}
	if !strings.Contains(got, "worktree parent unreadable") {
		t.Fatalf("the miss must name the cause; got:\n%s", got)
	}
}

// Same for the step after it: a verification plan that cannot be built is a
// guard that could not run, not an app that was never declared.
func TestCheckAppTrio_BuildVerifyFailureIsAMiss_NotASkip(t *testing.T) {
	root := appProfileRepo(t)
	originalResolve := checkAppTrioResolve
	checkAppTrioResolve = func(string) (*workspace.Target, error) {
		return &workspace.Target{MainRepo: root}, nil
	}
	t.Cleanup(func() { checkAppTrioResolve = originalResolve })
	originalVerify := checkAppTrioBuildVerify
	checkAppTrioBuildVerify = func(*workspace.Target, string) (*workspace.Verify, error) {
		return nil, fmt.Errorf("no verify plan for this checkout")
	}
	t.Cleanup(func() { checkAppTrioBuildVerify = originalVerify })

	var out, errb bytes.Buffer
	if checkAppTrio(root, &out, &errb) {
		t.Fatalf("an unbuildable verification plan must fail the guard; stdout:\n%s", out.String())
	}
	got := out.String()
	if strings.Contains(got, "[skip] no app declared") {
		t.Fatalf("the repo DOES declare an app; the guard must not claim otherwise; got:\n%s", got)
	}
	if !strings.Contains(got, "no verify plan for this checkout") {
		t.Fatalf("the miss must name the cause; got:\n%s", got)
	}
}

// The skip keeps its one honest case: a repo that declares no app profile at
// all is not charged for a check that does not apply to it.
func TestCheckAppTrio_UndeclaredAppStillSkips(t *testing.T) {
	root := t.TempDir()
	original := checkAppTrioResolve
	checkAppTrioResolve = func(string) (*workspace.Target, error) {
		t.Fatal("resolve must not run for a repo with no app profile")
		return nil, nil
	}
	t.Cleanup(func() { checkAppTrioResolve = original })

	var out, errb bytes.Buffer
	if !checkAppTrio(root, &out, &errb) {
		t.Fatalf("a repo with no app profile must pass the guard; stdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "check: app trio → [skip] no app declared") {
		t.Fatalf("stdout missing the no-app skip line, got:\n%s", out.String())
	}
}
