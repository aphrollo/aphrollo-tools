package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bashSuiteRoot makes a directory findRootFrom resolves as a project root,
// with no git and no suite actually runnable — DecideBashSuite never runs
// anything, so a bare go.mod marker is enough.
func bashSuiteRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module fixture\n\ngo 1.22\n")
	return dir
}

// decideBash runs DecideBashSuite over a Bash payload and fails the test if
// the payload was not judged at all — every case in this file names a
// command DecideBashSuite must recognise as a test-runner invocation.
func decideBash(t *testing.T, session, cwd, command string) Decision {
	t.Helper()
	d, judged := DecideBashSuite(bashPayload(t, session, cwd, command))
	if !judged {
		t.Fatalf("DecideBashSuite did not judge %q as a suite invocation", command)
	}
	return d
}

// An un-narrowed `go test ./...` beside a verdict the gate already holds for
// this tree answers nothing new: denying it is the whole point of the hook.
func TestDecideBashSuite_DeniesUnnarrowedWholeSuiteWithAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Block {
		t.Fatalf("want Block beside a fresh green verdict, got %v (reason %q)", d.Action, d.Reason)
	}
	if d.Policy != "bash-whole-suite" {
		t.Fatalf("want policy bash-whole-suite, got %q", d.Policy)
	}
}

// With NOTHING logged for the tree there is no answer to be redundant
// against, so the same un-narrowed invocation must flow — refusing it here
// would leave a session with no way to get a first verdict at all.
func TestDecideBashSuite_AllowsWholeSuiteWhenNoFreshVerdictExists(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Allow {
		t.Fatalf("want Allow with no verdict on record, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The sanctioned escape after an inconclusive TIMEOUT/SKIPPED line is a
// narrowed rerun, and it must stay allowed even beside a fresh verdict — the
// fresh verdict just makes the run notable enough to count.
func TestDecideBashSuite_AllowsANarrowedRerunAfterAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "timeout", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./...")
	if d.Action != Allow {
		t.Fatalf("a -run-narrowed rerun must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// cargo test's own narrowing shape (-p <crate> <filter>) must stay allowed
// too — the classifier is not go-test-only.
func TestDecideBashSuite_AllowsACargoPackageNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo test", "green", 0)

	d := decideBash(t, "s1", root, "cargo test -p widgets widget_roundtrip")
	if d.Action != Allow {
		t.Fatalf("a -p-narrowed cargo test must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// cargo nextest run's own -p narrowing must stay allowed too, matching
// cargo test's.
func TestDecideBashSuite_AllowsACargoNextestPackageNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo nextest run", "green", 0)

	d := decideBash(t, "s1", root, "cargo nextest run -p widgets")
	if d.Action != Allow {
		t.Fatalf("a -p-narrowed cargo nextest run must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// A bare `cargo nextest run` with no package or filter is the same
// whole-suite shape as `go test ./...` and must be denied on the same terms.
func TestDecideBashSuite_DeniesUnnarrowedCargoNextestWithAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("precommit", root, "cargo nextest run", "red", 0)

	d := decideBash(t, "s1", root, "cargo nextest run")
	if d.Action != Block {
		t.Fatalf("want Block, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The deny message is the whole point: a bare refusal invites a reworded
// resubmission, so it must name the verdict, its stage and the tool that
// answers the mutation half, not just say no.
func TestDecideBashSuite_DenyReasonNamesTheExistingVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	for _, want := range []string{"green", "postedit", "aphrollo gate stats", "aphrollo gate mutants status"} {
		if !strings.Contains(d.Reason, want) {
			t.Fatalf("deny reason %q must name %q", d.Reason, want)
		}
	}
}

// A deliberate soak is rare and expensive by nature, so every use is
// counted — like queue-bypass — regardless of what else is on record.
func TestDecideBashSuite_AllowsAndCountsADeliberateSoak(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	raw := bashPayload(t, "s1", root, "SOAK_SECS=600 go test ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a soak-marked go test must still be judged")
	}
	if d.Action != Allow {
		t.Fatalf("a soak must stay allowed, got %v", d.Action)
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-soak")
}

// An allowed narrowed rerun that runs beside a fresh verdict is exactly the
// escape hatch this hook must not let go uncounted — mirroring
// override-discard-env and queue-bypass, it gets its own gate.log line.
func TestDecideBashSuite_CountsANarrowedRerunBesideAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "timeout", 0)

	raw := bashPayload(t, "s1", root, "go test -run TestWidget ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a narrowed rerun must be judged")
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-narrowed")
}

// An ordinary narrowed rerun with nothing fresh on record is the mundane
// case (a session testing one function while writing it) and must NOT be
// counted, or every -run invocation would show up as an "override".
func TestDecideBashSuite_DoesNotCountAnOrdinaryNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	raw := bashPayload(t, "s1", root, "go test -run TestWidget ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a narrowed rerun must still be judged")
	}
	LogBashSuiteDecision(raw, d)
	if _, err := os.Stat(filepath.Join(cfg, "gate-state", "gate.log")); err == nil {
		t.Fatalf("an ordinary narrowed rerun must not write gate.log:\n%s", gateLogText(t, cfg))
	}
}

// PowerShell carries the same command shape under a different tool name
// (bashLikeTools, primary.go) and must be judged exactly like Bash.
func TestDecideBashSuite_JudgesPowerShellLikeBash(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d, judged := DecideBashSuite(powerShellPayload(t, "s1", root, "go test ./..."))
	if !judged {
		t.Fatal("a PowerShell whole-suite invocation must be judged")
	}
	if d.Action != Block {
		t.Fatalf("want Block for PowerShell too, got %v", d.Action)
	}
}

// An ordinary command that names no test runner at all is not this hook's
// concern and must not be judged (and therefore never logged).
func TestDecideBashSuite_IgnoresCommandsThatAreNotTestRunners(t *testing.T) {
	_, judged := DecideBashSuite(bashPayload(t, "s1", t.TempDir(), "git status"))
	if judged {
		t.Fatal("a non-test command must not be judged at all")
	}
}
