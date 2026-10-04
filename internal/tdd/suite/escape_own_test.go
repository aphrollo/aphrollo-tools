package suite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// These are suite's own tests of escape_suite.go, escape_verify_suite.go and
// clippyscope.go's cargoPackageDeps, reached today only through other
// packages' tests.

// TestDiffHeaderPath_ReadsThePostImagePath pins the header parse: the path
// after the last " b/", and nothing for any other line.
func TestDiffHeaderPath_ReadsThePostImagePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{"diff --git a/x.go b/x.go", "x.go", true},
		{"diff --git a/old.go b/dir/new.go", "dir/new.go", true},
		{"diff --git a/x.go", "", false},
		{"index 83db48f..bf2a3d1 100644", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := diffHeaderPath(c.line)
		if got != c.want || ok != c.ok {
			t.Errorf("diffHeaderPath(%q) = (%q, %v), want (%q, %v)", c.line, got, ok, c.want, c.ok)
		}
	}
}

// TestFixtureLawFromPath_NamesTheLawUnderTheFixturesDirectory pins the
// extraction: the segment right under .ratchet/fixtures/, and no law for any
// other path or for a file directly in the fixtures dir.
func TestFixtureLawFromPath_NamesTheLawUnderTheFixturesDirectory(t *testing.T) {
	t.Parallel()
	if law, ok := fixtureLawFromPath(".ratchet/fixtures/no-sleep/bad/case.go"); !ok || law != "no-sleep" {
		t.Errorf("law = (%q, %v), want (no-sleep, true)", law, ok)
	}
	for _, rel := range []string{"internal/x.go", ".ratchet/fixtures/loose.txt", ".ratchet/fixtures//x.go", ".ratchet/laws/no-sleep.toml"} {
		if law, ok := fixtureLawFromPath(rel); ok {
			t.Errorf("fixtureLawFromPath(%q) = (%q, true), want no law", rel, law)
		}
	}
}

// TestGhAvailable_IsWhetherGhResolvesOnPath pins the default probe against the
// same lookup (the package run puts a gh stub on PATH).
func TestGhAvailable_IsWhetherGhResolvesOnPath(t *testing.T) {
	_, err := exec.LookPath("gh")
	if got := ghAvailable(); got != (err == nil) {
		t.Fatalf("ghAvailable = %v, want %v", got, err == nil)
	}
}

// Serial: puts the fake gh on the process-wide PATH and env.
// ratchet: test_removed TestRunGh_ReturnsWhatGhPrintsAndRecordsItsArgv: runGh is the host port now; TestGitHubHost_ReturnsWhatGhPrintsAndRecordsItsArgv holds the same case
// TestGitHubHost_ReturnsWhatGhPrintsAndRecordsItsArgv pins the happy path
// through the stub: stdout comes back, and gh was called with exactly the args
// the port builds.
func TestGitHubHost_ReturnsWhatGhPrintsAndRecordsItsArgv(t *testing.T) {
	log := tddtest.StubGh(t, "issue-list-output")
	out, err := gitHubHost(t.TempDir(), 0).PRDiff("7")
	if err != nil || strings.TrimSpace(out) != "issue-list-output" {
		t.Fatalf("PRDiff = (%q, %v), want the stub's output", out, err)
	}
	if got := strings.TrimSpace(tddtest.GhArgv(t, log)); got != "pr diff 7" {
		t.Fatalf("gh was called as %q", got)
	}
}

// Serial: puts the fake gh on the process-wide PATH and env.
// ratchet: test_removed TestRunGhTimeout_AFailingGhCarriesItsStderrInTheError: runGhTimeout is the host port now; TestGitHubHost_AFailingGhCarriesItsStderrInTheError holds the same case
// TestGitHubHost_AFailingGhCarriesItsStderrInTheError pins the error text:
// the verb, the exit error and gh's own explanation.
func TestGitHubHost_AFailingGhCarriesItsStderrInTheError(t *testing.T) {
	tddtest.StubGh(t, "")
	tddtest.StubGhFail(t, "issue list", "label not found")
	_, err := gitHubHost(t.TempDir(), 0).ListIssues(host.IssueQuery{State: "open", Limit: 1, Fields: []string{"number"}})
	if err == nil || !strings.Contains(err.Error(), "gh issue:") || !strings.Contains(err.Error(), "label not found") {
		t.Fatalf("err = %v, want it to name the verb and carry gh's stderr", err)
	}
}

// Serial: puts the fake gh on the process-wide PATH and env.
// ratchet: test_removed TestRunGhTimeout_AGhThatCannotStartHasNoStderrToQuote: runGhTimeout is the host port now; TestGitHubHost_AGhThatCannotStartHasNoStderrToQuote holds the same case
// TestGitHubHost_AGhThatCannotStartHasNoStderrToQuote pins the bare error:
// with nothing on stderr the error is the verb and the cause alone.
func TestGitHubHost_AGhThatCannotStartHasNoStderrToQuote(t *testing.T) {
	tddtest.StubGh(t, "")
	_, err := gitHubHost(filepath.Join(t.TempDir(), "no-such-dir"), 0).ListIssues(host.IssueQuery{State: "open", Limit: 1, Fields: []string{"number"}})
	if err == nil || !strings.HasPrefix(err.Error(), "gh issue: ") {
		t.Fatalf("err = %v, want a bare gh error naming the verb", err)
	}
}

// Serial: puts the fake gh on the process-wide PATH and env.
// ratchet: test_removed TestRunGhTimeout_ASlowGhIsCutOffAtTheDeadline: runGhTimeout is the host port now; TestGitHubHost_ASlowGhIsCutOffAtTheDeadline holds the same case
// TestGitHubHost_ASlowGhIsCutOffAtTheDeadline pins the bound: a positive
// timeout kills a gh that outlasts it, and returns well before it would have.
func TestGitHubHost_ASlowGhIsCutOffAtTheDeadline(t *testing.T) {
	tddtest.StubGh(t, "late")
	t.Setenv("GH_STUB_SLEEP_MS", "20000")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := gitHubHost(t.TempDir(), 300*time.Millisecond).PRDiff("7")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a gh that outlasted its timeout must be an error")
		}
	case <-ctx.Done():
		t.Fatal("the host did not return within 30s of a 300ms timeout")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("returned after %s, want the timeout to cut the call short", elapsed)
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestHasGitHubRemote_IsWhetherAnyRemoteIsOnGitHub pins the check: a GitHub
// remote is one, another host is not, no remote is not, and neither is an empty
// repo path or a directory that is no repository.
func TestHasGitHubRemote_IsWhetherAnyRemoteIsOnGitHub(t *testing.T) {
	repo := makeGoRepo(t)
	if hasGitHubRemote(repo) {
		t.Error("a repo with no remote has no GitHub remote")
	}
	gitDo(t, repo, "remote", "add", "origin", "https://example.org/x/y.git")
	if hasGitHubRemote(repo) {
		t.Error("a non-GitHub remote is not a GitHub remote")
	}
	gitDo(t, repo, "remote", "add", "upstream", "https://github.com/x/y.git")
	if !hasGitHubRemote(repo) {
		t.Error("a github.com remote is a GitHub remote")
	}
	if hasGitHubRemote("") {
		t.Error("an empty repo path has none")
	}
	if hasGitHubRemote(t.TempDir()) {
		t.Error("a directory that is no repository has none")
	}
}

// TestCargoPackageDeps_NoWorkspaceHasNothingToAsk pins the quiet arm.
func TestCargoPackageDeps_NoWorkspaceHasNothingToAsk(t *testing.T) {
	t.Parallel()
	deps, err := cargoPackageDeps("")
	if deps != nil || err != nil {
		t.Fatalf("cargoPackageDeps = (%v, %v), want (nil, nil)", deps, err)
	}
}

// Serial: points CARGO at a fake in the process-wide environment.
// TestCargoPackageDeps_AMissingCargoIsAReadFailureNotAnEmptyGraph pins the
// loud arm: the two mean opposite things for coverage.
func TestCargoPackageDeps_AMissingCargoIsAReadFailureNotAnEmptyGraph(t *testing.T) {
	t.Setenv("CARGO", filepath.Join(t.TempDir(), "no-such-cargo"))
	deps, err := cargoPackageDeps(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "cargo metadata") || deps != nil {
		t.Fatalf("cargoPackageDeps = (%v, %v), want a cargo metadata error", deps, err)
	}
}

// Serial: points CARGO at a fake in the process-wide environment.
// TestCargoPackageDeps_ParsesWhatCargoMetadataAnswers pins the success path:
// the workspace's own graph keyed by package name.
func TestCargoPackageDeps_ParsesWhatCargoMetadataAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		// skip-ok: the fake cargo is a POSIX shell script; every assertion runs on each POSIX box.
		t.Skip("the fake cargo is a POSIX shell script")
	}
	doc := `{"packages":[` +
		`{"name":"app","dependencies":[{"name":"core"},{"name":"serde"}]},` +
		`{"name":"core","dependencies":[]}]}`
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + doc + "\nEOF\n"
	if err := proc.WriteExecutable(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", filepath.Join(dir, "cargo"))

	deps, err := cargoPackageDeps(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(deps["app"]) != 1 || deps["app"][0] != "core" {
		t.Fatalf("deps = %v, want app -> [core] (serde is outside the workspace)", deps)
	}
}

// Serial: puts a fake cargo on the process-wide PATH and clears CARGO.
// TestCargoPackageDeps_DefaultsToTheCargoOnPathWhenCargoIsUnset pins the
// default binary name: with $CARGO empty the lookup is plain "cargo".
func TestCargoPackageDeps_DefaultsToTheCargoOnPathWhenCargoIsUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		// skip-ok: the fake cargo is a POSIX shell script; every assertion runs on each POSIX box.
		t.Skip("the fake cargo is a POSIX shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho '{\"packages\":[{\"name\":\"only\",\"dependencies\":[]}]}'\n"
	if err := proc.WriteExecutable(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := cargoPackageDeps(t.TempDir()); err != nil {
		t.Fatalf("cargoPackageDeps with the fake on PATH: %v", err)
	}
}
