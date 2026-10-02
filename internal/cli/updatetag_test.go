package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

func TestNewestReleaseTag_PicksTheHighestSemverNotTheLexicalLast(t *testing.T) {
	got, ok := newestReleaseTag([]string{"v0.9.0", "v1.10.0", "v1.2.0", "v1.9.9"})
	if !ok || got != "v1.10.0" {
		t.Fatalf("newest = %q, %v; want v1.10.0", got, ok)
	}
}

func TestNewestReleaseTag_IgnoresTagsThatAreNotReleases(t *testing.T) {
	got, ok := newestReleaseTag([]string{"salvage/install", "v2.0.0-rc1", "v3", "1.9.0", "v1.2.3"})
	if !ok || got != "v1.2.3" {
		t.Fatalf("newest = %q, %v; want v1.2.3", got, ok)
	}
}

func TestNewestReleaseTag_ReportsNoneWhenThereAreNoReleaseTags(t *testing.T) {
	if got, ok := newestReleaseTag([]string{"salvage/x", "v2"}); ok {
		t.Fatalf("newest = %q, want none", got)
	}
}

func stubBuild(t *testing.T, record *string) {
	t.Helper()
	git := realGitForTest(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		*record = gitOutput(t, git, repo, "rev-parse", "HEAD")
		return "go build", os.WriteFile(out, []byte("NEW"), 0o755)
	}
	t.Cleanup(func() { buildAphrollo = prev })
}

// main moves past the newest tag; the update must still build the tag.
func TestUpdate_BuildsTheNewestTagNotMainWhenMainIsAhead(t *testing.T) {
	_, clone, seed := updateFixture(t)
	git := realGitForTest(t)
	tagged := gitOutput(t, git, seed, "rev-parse", "v1.0.0")
	mustWriteFile(t, filepath.Join(seed, "ahead.txt"), "x")
	gitOutput(t, git, seed, "add", "-A")
	gitOutput(t, git, seed, "commit", "-q", "-m", "ahead of the tag")
	gitOutput(t, git, seed, "push", "-q", "origin", "main")

	var built string
	stubBuild(t, &built)
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	mustWriteFile(t, bin, "OLD")

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s", code, errb.String())
	}
	if built != tagged {
		t.Fatalf("built %s, want the v1.0.0 commit %s", built, tagged)
	}
}

func TestUpdate_NamesTheTagItMovedFromAndTo(t *testing.T) {
	_, clone, _ := updateFixture(t)
	var built string
	stubBuild(t, &built)
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	mustWriteFile(t, bin, "OLD")

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s", code, errb.String())
	}
	want := "aphrollo update: v" + buildinfo.Version() + " -> v1.0.0\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout lacks %q:\n%s", want, out.String())
	}
}

func TestUpdate_RefusesWithAFixWhenTheRemoteHasNoReleaseTag(t *testing.T) {
	_, clone, seed := updateFixture(t)
	git := realGitForTest(t)
	gitOutput(t, git, seed, "push", "-q", "origin", ":refs/tags/v1.0.0")
	gitOutput(t, git, clone, "tag", "-d", "v1.0.0")
	var built string
	stubBuild(t, &built)
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	mustWriteFile(t, bin, "OLD")

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "no release tag") {
		t.Fatalf("exit = %d, stderr = %q; want 1 naming 'no release tag'", code, errb.String())
	}
	if built != "" {
		t.Fatal("built without a release tag")
	}
}
