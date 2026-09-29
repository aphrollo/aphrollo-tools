package postedit

import (
	"os"
	"os/exec"
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

// ratchet: test_removed TestDecideBashSuite_FallsBackToTheSessionCwdWhenTheCdIsUnresolvable: the fallback was the cause of issue #953; replaced by TestDecideBashSuite_AllowsARunWhenTheCdIsUnresolvable

// A cd this scanner cannot resolve (a variable, `cd -`) leaves the run
// directory unknown, and an unknown one is answered with NO root: the gate
// holds no verdict for a tree it cannot name, and refusing on the session
// cwd's verdict instead attributed a run in an independent clone to the
// primary checkout (issue #953).
func TestDecideBashSuite_AllowsARunWhenTheCdIsUnresolvable(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	AppendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "cd $LANE && go test -run TestWidget ./...")
	if d.Action != Allow {
		t.Fatalf("a run in a tree the cd names by variable must not be refused on the cwd's verdict, got %v", d.Action)
	}
}

// independentClone makes a real `git clone` of primary at a path unrelated to
// it, the shape issue #953 reported: its own .git directory, not a worktree
// of the primary, and no path prefix in common.
func independentClone(t *testing.T, primary string) string {
	t.Helper()
	runGit(t, primary, "init", "-q")
	runGit(t, primary, "add", ".")
	runGit(t, primary, "-c", "core.hooksPath=/dev/null", "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
	clone := filepath.Join(t.TempDir(), "tmp", "base")
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(clone), "clone", "-q", primary, clone)
	return clone
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
}

// A hand run in an independent clone must never be refused on a verdict held
// for the checkout the session stands in (issue #953). Every spelling of
// "enter the clone" the scanner cannot follow used to fall back to the
// session's cwd, the primary: `cd -- dir`, `pushd`, a subshell, a variable,
// and a Windows path whose backslashes an unquoted word swallows.
func TestDecideBashSuite_AllowsARunInAnIndependentCloneWhateverTheCdSpelling(t *testing.T) {
	primary := bashSuiteRoot(t)
	clone := independentClone(t, primary)
	slash := filepath.ToSlash(clone)
	run := "go test -run TestWidget ./..."
	for name, cmd := range map[string]string{
		"double dash":       "cd -- " + slash + " && " + run,
		"pushd":             "pushd " + slash + " && " + run,
		"subshell":          "(cd " + slash + " && " + run + ")",
		"variable":          "cd $CLONE && " + run,
		"windows backslash": `cd C:\Users\olive\tmp\base && ` + run,
		"missing dir":       "cd no/such/dir && " + run,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			AppendGateLog("premergecommit", primary, "go test ./...", "mechanical-blocked", 0)
			d := decideBash(t, "s1", primary, cmd)
			if d.Action != Allow {
				t.Fatalf("%q run in the clone must not be refused on the primary's verdict, got %v (reason %q)",
					cmd, d.Action, d.Reason)
			}
		})
	}
}

// The clone's own verdict still refuses a redundant run there: identity, not
// a blanket waiver.
func TestDecideBashSuite_StillDeniesARerunInTheCloneTheVerdictWasLoggedFor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary := bashSuiteRoot(t)
	clone := independentClone(t, primary)
	AppendGateLog("postedit", clone, "go test ./...", "green", 0)

	d := decideBash(t, "s1", primary, "cd "+filepath.ToSlash(clone)+" && go test -run TestWidget ./...")
	if d.Action != Block {
		t.Fatalf("a rerun in the tree the verdict names must stay refused, got %v", d.Action)
	}
}

// A command line that runs suites in two different trees has no single root
// to judge; the session's cwd verdict is not the answer for either.
func TestDecideBashSuite_AllowsRunsInTwoTreesBesideTheCwdsVerdict(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary := bashSuiteRoot(t)
	clone := independentClone(t, primary)
	other := bashSuiteRoot(t)
	AppendGateLog("postedit", primary, "go test ./...", "green", 0)

	cmd := "cd " + filepath.ToSlash(clone) + " && go test -run TestA ./... && cd " + filepath.ToSlash(other) + " && go test -run TestB ./..."
	d := decideBash(t, "s1", primary, cmd)
	if d.Action != Allow {
		t.Fatalf("runs in two trees must not be refused on the cwd's verdict, got %v", d.Action)
	}
}
