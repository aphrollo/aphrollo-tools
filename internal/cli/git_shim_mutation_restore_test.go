package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Issue #650's second and third defects, both about the restore half of a
// hand mutation proof: `git checkout -- <file>` was refused by the discard
// wall on every attempt (so the mutations accumulated and the proof pass
// proved nothing), and under APHROLLO_DISCARD=1 it restored the file to HEAD,
// deleting the unstaged work the lane had in it. The proof's restore is the
// PROTECTIVE step; the wall was treating it as a discard of work.

// mutationProofRepo builds the mid-lane state a proof actually starts from: a
// committed file that also carries uncommitted, unstaged work of its own.
func mutationProofRepo(t *testing.T, session string) (repo string, cfg gitShimConfig, file string) {
	t.Helper()
	repo, cfg = discardWallFixture(t)
	t.Setenv("CLAUDE_SESSION_ID", session)
	writeFixtureFile(t, repo, "f.txt", []string{"committed"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "committed base")
	writeFixtureFile(t, repo, "f.txt", []string{"committed", "lane work"})
	return repo, cfg, filepath.Join(repo, "f.txt")
}

func readFixtureFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestGitShim_MutationProofRestoresTheWorkingStateNotHEAD(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg, file := mutationProofRepo(t, "s-mutation-restore")
	if _, err := tdd.HoldMutation(file); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, "f.txt", []string{"committed", "lane work", "one-line mutation"})
	t.Setenv(mutationProofEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "f.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("restore inside a declared mutation proof: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got, want := readFixtureFile(t, file), "committed\nlane work\n"; got != want {
		t.Fatalf("f.txt = %q, want the WORKING state the proof started from (%q) — restoring to HEAD is what deleted the lane's unstaged work", got, want)
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "override-discard-mutation-proof") {
		t.Fatalf("gate.log = %q, want the mutation-proof restore counted, exactly as the test gate's marker is", log)
	}
}

func TestGitShim_MutationProofRestoreRefusesWithNoHold(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, file := mutationProofRepo(t, "s-mutation-nohold")
	writeFixtureFile(t, repo, "f.txt", []string{"committed", "lane work", "one-line mutation"})
	t.Setenv(mutationProofEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "f.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("restore with no hold: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if got := readFixtureFile(t, file); !strings.Contains(got, "one-line mutation") {
		t.Fatalf("f.txt = %q, want it untouched: a proof that corrupts the tree is worse than one that does not run", got)
	}
	if !strings.Contains(errb.String(), "f.txt") || !strings.Contains(errb.String(), "mutants hold") {
		t.Fatalf("refusal = %q, want it to name the file and the verb that takes a hold", errb.String())
	}
}

func TestGitShim_MutationProofRestoresAnUntrackedFile(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, _ := mutationProofRepo(t, "s-mutation-untracked")
	untracked := filepath.Join(repo, "new.txt")
	writeFixtureFile(t, repo, "new.txt", []string{"untracked v1"})
	if _, err := tdd.HoldMutation(untracked); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, "new.txt", []string{"untracked mutated"})
	t.Setenv(mutationProofEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "new.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("restore of an untracked file inside a proof: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got, want := readFixtureFile(t, untracked), "untracked v1\n"; got != want {
		t.Fatalf("new.txt = %q, want %q: git checkout -- never restored an untracked file at all", got, want)
	}
}

func TestGitShim_MutationProofMarkerDoesNotWidenToOtherVerbs(t *testing.T) {
	gateConfigDir(t)
	_, cfg, file := mutationProofRepo(t, "s-mutation-no-widening")
	if _, err := tdd.HoldMutation(file); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mutationProofEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("reset --hard under the mutation marker: exit = %d, want 1 — the marker covers a restore of a held file, not every discarding verb\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(readFixtureFile(t, file), "lane work") {
		t.Fatal("reset --hard must not have run")
	}
}

func TestGitShim_MutationProofRestoreRefusesAStaleHold(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, file := mutationProofRepo(t, "s-mutation-stale")
	hold, err := tdd.HoldMutation(file)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, "f.txt", []string{"committed", "lane work", "one-line mutation"})
	restoreClock := tdd.SetMutationHoldClockForTest(func() time.Time {
		return hold.At.Add(tdd.MutationHoldTTL + time.Hour)
	})
	defer restoreClock()
	t.Setenv(mutationProofEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "f.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("restore from a stale hold: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(readFixtureFile(t, file), "one-line mutation") {
		t.Fatal("a refused restore must leave the file exactly as it found it")
	}
}

func TestGitShim_WithoutTheMarkerTheRefusalPointsAtTheHold(t *testing.T) {
	gateConfigDir(t)
	repo, cfg, file := mutationProofRepo(t, "s-mutation-unmarked")
	if _, err := tdd.HoldMutation(file); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, "f.txt", []string{"committed", "lane work", "one-line mutation"})

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "f.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("unmarked restore of a held file: exit = %d, want 1 (the ordinary discard refusal)\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), mutationProofEnv+"=1") {
		t.Fatalf("refusal = %q, want it to point at the marker: this session holds a pre-mutation state for that very file", errb.String())
	}
}
