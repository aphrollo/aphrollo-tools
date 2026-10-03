package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --dry prints the fetch ref, the build target and the swap path and does
// none of them: the clone's remote ref does not move, the build seam never
// runs and the binary keeps its bytes.
func TestUpdate_DryPrintsThePlanAndFetchesBuildsAndSwapsNothing(t *testing.T) {
	origin, clone, seed := updateFixture(t)
	git := realGitForTest(t)
	before := gitOutput(t, git, clone, "rev-parse", "origin/main")
	mustWriteFile(t, filepath.Join(seed, "next.txt"), "x")
	gitOutput(t, git, seed, "add", "-A")
	gitOutput(t, git, seed, "commit", "-q", "-m", "next")
	gitOutput(t, git, seed, "push", "-q", "origin", "main")
	_ = origin

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init", "--dry"}, &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	for _, want := range []string{"fetch: origin tags, newest v<MAJOR.MINOR.PATCH> in " + clone, "build: ./cmd/aphrollo", "swap:  " + bin} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	if called {
		t.Error("--dry ran the build")
	}
	if got := gitOutput(t, git, clone, "rev-parse", "origin/main"); got != before {
		t.Errorf("--dry fetched: origin/main moved %s -> %s", before, got)
	}
	if got, err := os.ReadFile(bin); err != nil || string(got) != "OLD" {
		t.Errorf("the binary changed under --dry: %q (%v)", got, err)
	}
}

// A "--" in first place carries no update flag before it: everything after it
// is init's, so the verb goes on to its own checks. The install refusal is how
// the test sees it got that far, without a fetch, build or swap.
func TestUpdate_ADoubleDashFirstForwardsEverythingAfterIt(t *testing.T) {
	_, clone, _ := updateFixture(t)
	t.Chdir(clone)
	prevWritable := installWritable
	installWritable = func(string) bool { return false }
	t.Cleanup(func() { installWritable = prevWritable })

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--", "--git-hooks-dir", t.TempDir()}, &out, &errb)

	if code != 1 || !strings.Contains(errb.String(), "not writable") {
		t.Fatalf("exit %d, stderr %q; want the install refusal (exit 1)", code, errb.String())
	}
}

func TestUpdate_UnknownFlagBeforeTheDoubleDashIsRefused(t *testing.T) {
	var out, errb bytes.Buffer

	code := runUpdate([]string{"--bogus", "--", "--git-hooks-dir", "x"}, &out, &errb)

	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr: %s", code, errb.String())
	}
}

func TestUpdate_StrayArgumentIsRefused(t *testing.T) {
	var out, errb bytes.Buffer

	code := runUpdate([]string{"stray", "--dry"}, &out, &errb)

	if code != 2 || !strings.Contains(errb.String(), `unexpected argument "stray"`) {
		t.Fatalf("exit %d, stderr %q; want 2 naming the stray argument", code, errb.String())
	}
}
