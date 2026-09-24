package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Issue #814, from a borld bisect: with a mutants hold active on three files,
//
//	MUTATION=1 git checkout HEAD~1 -- crates/forge_solver/src/particle/force.rs crates/forge_solver/src/rigid/force.rs crates/forge_solver/src/particle/bdf2.rs
//
// answered "restored 3 file(s) from the working state held 0s ago": the tree
// went back to the held state, not to HEAD~1, and the bisect step was a
// silent no-op. The held-state restore is the answer to ONE question — put
// back what the proof started from — and a command naming a revision asks a
// different one, which git answers.

// heldBisectRepo commits three files twice (v1, then v2), leaves the tree
// clean at v2, and takes a hold of all three, as a bisect with a hold still
// active from an earlier proof has it.
func heldBisectRepo(t *testing.T, session string) (repo string, cfg gitShimConfig, paths []string) {
	t.Helper()
	repo, cfg = discardWallFixture(t)
	t.Setenv("CLAUDE_SESSION_ID", session)
	paths = []string{"src/particle/force.rs", "src/rigid/force.rs", "src/particle/bdf2.rs"}
	for _, dir := range []string{"src/particle", "src/rigid"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"v1", "v2"} {
		for _, p := range paths {
			writeFixtureFile(t, repo, p, []string{p + " " + v})
		}
		runFixtureGit(t, cfg.realGit, repo, "add", ".")
		runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", v)
	}
	for _, p := range paths {
		if _, err := tdd.HoldMutation(filepath.Join(repo, p)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(mutationProofEnv, "1")
	return repo, cfg, paths
}

func requireCheckedOutV1(t *testing.T, repo string, paths []string, code int, stderr string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, want 0: a checkout of a named revision runs as typed\nstderr: %s", code, stderr)
	}
	for _, p := range paths {
		if got, want := readFixtureFile(t, filepath.Join(repo, p)), p+" v1\n"; got != want {
			t.Fatalf("%s = %q, want HEAD~1's %q — the held working state answered a checkout of another revision\nstderr: %s", p, got, want, stderr)
		}
	}
	if strings.Contains(stderr, "restored") {
		t.Fatalf("stderr = %q, want no held-state restore reported for a checkout of a named revision", stderr)
	}
}

func TestGitShim_MutationMarkerCheckoutOfARevisionChecksOutThatRevision(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, paths := heldBisectRepo(t, "s-mutation-rev-checkout")

	var out, errb bytes.Buffer
	code := runGitShim(append([]string{"checkout", "HEAD~1", "--"}, paths...), strings.NewReader(""), &out, &errb, cfg)

	requireCheckedOutV1(t, repo, paths, code, errb.String())
}

func TestGitShim_MutationMarkerRestoreFromSourceEqualsRestoresThatSource(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, paths := heldBisectRepo(t, "s-mutation-rev-source-eq")

	var out, errb bytes.Buffer
	code := runGitShim(append([]string{"restore", "--source=HEAD~1"}, paths...), strings.NewReader(""), &out, &errb, cfg)

	requireCheckedOutV1(t, repo, paths, code, errb.String())
}

func TestGitShim_MutationMarkerRestoreFromDashSRestoresThatSource(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, paths := heldBisectRepo(t, "s-mutation-rev-dash-s")

	var out, errb bytes.Buffer
	code := runGitShim(append([]string{"restore", "-s", "HEAD~1"}, paths...), strings.NewReader(""), &out, &errb, cfg)

	requireCheckedOutV1(t, repo, paths, code, errb.String())
}

// The form the hold exists for still serves it, and its line names each file
// by its path — two force.rs files are two different files — and says the
// bytes came from the held working state rather than any revision.
func TestGitShim_MutationRestoreLineNamesEachFileByPath(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, paths := heldBisectRepo(t, "s-mutation-line")
	for _, p := range paths {
		writeFixtureFile(t, repo, p, []string{p + " mutated"})
	}

	var out, errb bytes.Buffer
	code := runGitShim(append([]string{"checkout", "--"}, paths...), strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("restore of held files: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	for _, p := range paths {
		if got, want := readFixtureFile(t, filepath.Join(repo, p)), p+" v2\n"; got != want {
			t.Fatalf("%s = %q, want the held working state %q", p, got, want)
		}
		if !strings.Contains(errb.String(), p) {
			t.Fatalf("restore line = %q, want it to name %s by its path", errb.String(), p)
		}
	}
	if !strings.Contains(errb.String(), "not from HEAD, the index or any revision") {
		t.Fatalf("restore line = %q, want it to say what the bytes came from and what they did not", errb.String())
	}
}

// Every spelling git accepts for restore's source names a revision: the
// separate long form, the value attached to -s, and -s bundled after another
// short flag.
func TestGitShim_MutationMarkerRestoreSourceSpellingsRestoreThatSource(t *testing.T) {
	for _, spelling := range [][]string{
		{"--source", "HEAD~1"},
		{"-sHEAD~1"},
		{"-Ws", "HEAD~1"},
	} {
		t.Run(strings.Join(spelling, " "), func(t *testing.T) {
			gateConfigDir(t)
			repo, cfg, paths := heldBisectRepo(t, "s-mutation-rev-spelling")

			var out, errb bytes.Buffer
			args := append(append([]string{"restore"}, spelling...), paths...)
			code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg)

			requireCheckedOutV1(t, repo, paths, code, errb.String())
		})
	}
}

// A long option that merely contains the letter s names no revision: the
// restore it asks for is still the held one.
func TestGitShim_MutationRestoreWithALongOptionStillServesTheHold(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, paths := heldBisectRepo(t, "s-mutation-long-option")
	for _, p := range paths {
		writeFixtureFile(t, repo, p, []string{p + " mutated"})
	}

	var out, errb bytes.Buffer
	code := runGitShim(append([]string{"restore", "--progress"}, paths...), strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("restore --progress of held files: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	for _, p := range paths {
		if got, want := readFixtureFile(t, filepath.Join(repo, p)), p+" v2\n"; got != want {
			t.Fatalf("%s = %q, want the held working state %q", p, got, want)
		}
	}
}
