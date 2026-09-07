package tdd

import (
	"bytes"
	"testing"
)

// The trailer is git's own, matched the way `git interpret-trailers` matches
// it: key case-insensitive, and here the value too, so a session that typed
// `mutants: RUN` gets the same answer as one that typed `Mutants: run`.
func TestMutantsRunRequested_TrueForATrailerCaseInsensitiveOnKeyAndValue(t *testing.T) {
	root := makeCargoRepo(t)
	gitDo(t, root, "commit", "--allow-empty", "-qm", "tweak\n\nmutants: RUN")
	requested, err := mutantsRunRequested(root)
	if err != nil {
		t.Fatalf("mutantsRunRequested returned an error for a readable HEAD: %v", err)
	}
	if !requested {
		t.Fatal("a case-varied `mutants: RUN` trailer must still count")
	}
}

// The common case: an ordinary commit with no trailer at all asks for
// nothing, which is the whole point of issue #521 — inference is replaced by
// an explicit ask, and most commits do not make one.
func TestMutantsRunRequested_FalseWithNoTrailer(t *testing.T) {
	root := makeCargoRepo(t)
	gitDo(t, root, "commit", "--allow-empty", "-qm", "an ordinary commit")
	requested, err := mutantsRunRequested(root)
	if err != nil {
		t.Fatalf("mutantsRunRequested returned an error for a readable HEAD: %v", err)
	}
	if requested {
		t.Fatal("a commit with no trailer must not read as a request")
	}
}

// A git failure is NOT the same answer as "no trailer": the caller needs to
// tell the two apart, or a transient failure to read HEAD silently drops a
// run the author explicitly asked for, with nothing logged — the one case
// this trigger exists to serve. Proven directly against a repo with no
// commits at all, where `git log -1 ... HEAD` cannot resolve HEAD and fails.
func TestMutantsRunRequested_ReportsAGitFailureRatherThanReportingNoTrailer(t *testing.T) {
	root := t.TempDir()
	gitDo(t, root, "init", "-q")
	requested, err := mutantsRunRequested(root)
	if err == nil {
		t.Fatal("mutantsRunRequested on a repo with no commits must return an error, not silently answer false")
	}
	if requested {
		t.Fatal("an errored call must not also claim a run was requested")
	}
}

// Git's own trailer parser requires a trailer-shaped final paragraph — a
// blank line before it, then "Key: value" lines only. Words that merely
// mention "Mutants: run" inside an ordinary sentence, with no such block,
// must not be read as the signal: a commit message that happens to discuss
// the feature must not accidentally start a run.
func TestMutantsRunRequested_FalseWhenTheWordsAppearOnlyInBodyProse(t *testing.T) {
	root := makeCargoRepo(t)
	gitDo(t, root, "commit", "--allow-empty", "-qm",
		"document the trailer\n\nExplains that writing Mutants: run starts a job, without itself asking for one.\n\nSigned-off-by: t <t@t>")
	requested, err := mutantsRunRequested(root)
	if err != nil {
		t.Fatalf("mutantsRunRequested returned an error for a readable HEAD: %v", err)
	}
	if requested {
		t.Fatal("prose mentioning the trailer's own name must not itself count as the trailer")
	}
}

// The integration point: an opted-in lane commit that carries no trailer must
// not start a detached run at all — the behaviour issue #521 changes. Every
// other refusal reason (branch, opt-in, CI-only, disk) is exercised
// elsewhere; this is the one this issue adds.
func TestStartMutantsJob_RefusesAnOptedInLaneCommitWithNoMutantsRunTrailer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work, no trailer")

	if _, ok := StartMutantsJob(root); ok {
		t.Fatal("a lane commit with no `Mutants: run` trailer must not start a detached run")
	}
	if len(started) != 0 {
		t.Fatalf("spawned %d job(s) with no trailer asking for one", len(started))
	}
}

// The bare fallback named in the design: `aphrollo gate mutants run`, typed
// by hand, must keep working with no trailer at all — it IS the explicit
// signal, the other one issue #521 names beside the trailer.
func TestRunMutantsHere_IgnoresTheTrailerRequirement(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work, no trailer")

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code != 0 {
		t.Fatalf("RunMutantsHere with no trailer = %d, want 0 — hand-typed is its own explicit signal\noutput: %s", code, out.String())
	}
	if len(ran) != 1 {
		t.Fatalf("ran %d jobs, want 1", len(ran))
	}
}
