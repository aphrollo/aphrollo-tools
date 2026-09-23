package postedit

import (
	"path/filepath"
	"strings"
	"testing"
)

// A hand run issued inside a LANE worktree must not be refused on a verdict
// the gate logged for the PRIMARY checkout: the two are different trees, and
// the primary's green says nothing about whether the lane's suite has ever
// run on the lane's commit. Issue #645 — the harness resets a session's cwd
// to the primary between tool calls, so `cd <lane> && cargo nextest run …`
// was judged against the primary's root and refused, for every narrowing, on
// a verdict from another session's unrelated merge.
func TestDecideBashSuite_AllowsALaneRerunBesideThePrimarysVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	primary := bashSuiteRoot(t)
	lane := bashSuiteRoot(t)
	AppendGateLog("premergecommit", primary, "cargo nextest run", "green", 0)

	cmd := "cd " + filepath.ToSlash(lane) + " && cargo nextest run -p server -p client"
	d := decideBash(t, "s1", primary, cmd)
	if d.Action != Allow {
		t.Fatalf("a run in %s must not be refused on a verdict for %s, got %v (reason %q)",
			lane, primary, d.Action, d.Reason)
	}
}

// The same fix for the un-narrowed shape: a whole-suite run in a lane the
// gate holds nothing for is the lane's FIRST answer, and the primary's fresh
// green is not it.
func TestDecideBashSuite_AllowsALaneWholeSuiteBesideThePrimarysVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	primary := bashSuiteRoot(t)
	lane := bashSuiteRoot(t)
	AppendGateLog("premergecommit", primary, "go test ./...", "green", 0)

	cmd := "cd " + filepath.ToSlash(lane) + " && go test ./..."
	d := decideBash(t, "s1", primary, cmd)
	if d.Action != Allow {
		t.Fatalf("a whole suite in %s must not be refused on a verdict for %s, got %v (reason %q)",
			lane, primary, d.Action, d.Reason)
	}
}

// Checkout identity is one condition, not a way out of the other: a narrowed
// rerun that lands in the SAME tree as the fresh verdict is still the
// redundant run issue #572 refused, whether the session names that tree with
// a `cd` or stands in it already.
func TestDecideBashSuite_StillDeniesANarrowedRerunReachedByCdIntoTheSameRoot(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	elsewhere := t.TempDir()
	AppendGateLog("postedit", root, "go test ./...", "green", 0)

	cmd := "cd " + filepath.ToSlash(root) + " && go test -run TestWidget ./..."
	d := decideBash(t, "s1", elsewhere, cmd)
	if d.Action != Block {
		t.Fatalf("a narrowed rerun inside the verdict's own tree must stay refused, got %v", d.Action)
	}
}

// A `cd` into a SUBDIRECTORY of the verdict's tree is the same checkout —
// the root walk answers that, so the refusal holds there too.
func TestDecideBashSuite_StillDeniesAWholeSuiteRunFromASubdirectoryOfTheVerdictsRoot(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	write(t, root, filepath.Join("internal", "widget", "widget.go"), "package widget\n")
	sub := filepath.Join(root, "internal", "widget")
	AppendGateLog("postedit", root, "go test ./...", "green", 0)

	cmd := "cd " + filepath.ToSlash(sub) + " && go test ./..."
	d := decideBash(t, "s1", root, cmd)
	if d.Action != Block {
		t.Fatalf("a whole-suite run under the verdict's own root must stay refused, got %v", d.Action)
	}
}

// cargo names its tree with --manifest-path instead of a cd; the directory
// that manifest lives in is where the run lands.
func TestDecideBashSuite_ReadsTheRunDirFromCargosManifestPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	primary := bashSuiteRoot(t)
	lane := bashSuiteRoot(t)
	AppendGateLog("premergecommit", primary, "cargo test", "green", 0)

	cmd := "cargo test --manifest-path " + filepath.ToSlash(filepath.Join(lane, "Cargo.toml")) + " -p server"
	d := decideBash(t, "s1", primary, cmd)
	if d.Action != Allow {
		t.Fatalf("a --manifest-path run in %s must not be refused on %s's verdict, got %v (reason %q)",
			lane, primary, d.Action, d.Reason)
	}
}

// go test's own -C moves the run the same way a cd does.
func TestDecideBashSuite_ReadsTheRunDirFromGoTestsChangeDirFlag(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	primary := bashSuiteRoot(t)
	lane := bashSuiteRoot(t)
	AppendGateLog("postedit", primary, "go test ./...", "green", 0)

	cmd := "go test -C " + filepath.ToSlash(lane) + " ./..."
	d := decideBash(t, "s1", primary, cmd)
	if d.Action != Allow {
		t.Fatalf("a -C run in %s must not be refused on %s's verdict, got %v (reason %q)",
			lane, primary, d.Action, d.Reason)
	}
}

// `git -C <dir>` scopes ONE git process; it does not move the shell, so the
// suite that follows it still runs where the session stands. Reading it as a
// cd would send the guard looking at a tree the runner never enters.
func TestDecideBashSuite_DoesNotReadGitDashCAsACdForTheRunner(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	elsewhere := bashSuiteRoot(t)
	AppendGateLog("postedit", root, "go test ./...", "green", 0)

	cmd := "git -C " + filepath.ToSlash(elsewhere) + " status && go test -run TestWidget ./..."
	d := decideBash(t, "s1", root, cmd)
	if d.Action != Block {
		t.Fatalf("the runner still stands in %s, so its rerun stays refused, got %v", root, d.Action)
	}
}

// What the gate LOGS about a refused run has to name the tree the run would
// have entered: a line blaming the session's cwd files a lane's denied run
// against the primary checkout, where `gate stats` reads it as pressure on
// the wrong tree.
func TestDecideBashSuite_LogsADeniedLaneRunAgainstTheLanesRoot(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	primary := bashSuiteRoot(t)
	lane := bashSuiteRoot(t)
	AppendGateLog("postedit", lane, "go test ./...", "green", 0)

	raw := bashPayload(t, "s1", primary, "cd "+filepath.ToSlash(lane)+" && go test ./...")
	d, judged := DecideBashSuite(raw)
	if !judged || d.Action != Block {
		t.Fatalf("the lane's own fresh green must refuse this run, got judged=%v %v", judged, d.Action)
	}
	LogBashSuiteDecision(raw, d)
	text := gateLogText(t, cfg)
	if !strings.Contains(text, LogToken(lane)) {
		t.Fatalf("gate.log must name %s as the denied run's root:\n%s", lane, text)
	}
}

// A cd this scanner cannot resolve (a variable, `cd -`) leaves the run
// directory unknown, and an unknown one falls back to the session's cwd —
// today's behaviour — rather than to a guess that would waive the guard.
func TestDecideBashSuite_FallsBackToTheSessionCwdWhenTheCdIsUnresolvable(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	AppendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "cd $LANE && go test -run TestWidget ./...")
	if d.Action != Block {
		t.Fatalf("an unresolvable cd must fall back to the cwd's verdict, got %v", d.Action)
	}
}
