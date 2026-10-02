package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseRepo is a repo with a VERSION file and one commit, and the bash that
// runs deploy/*.sh against it (the test skips where there is no bash).
func releaseRepo(t *testing.T, version string) (repo string, sh func(script string, env ...string) (string, error)) {
	t.Helper()
	isolateGitConfigCLI(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH") // skip-ok: the scripts under test are bash
	}
	git := realGitForTest(t)
	repo = t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "internal", "buildinfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(repo, "internal", "buildinfo", "VERSION"), version+"\n")
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		gitOutput(t, git, repo, args...)
	}
	scripts, err := filepath.Abs(filepath.Join("..", "..", "deploy")) // tree-read-ok: the scripts under test are this tree's own
	if err != nil {
		t.Fatal(err)
	}
	sh = func(script string, env ...string) (string, error) {
		cmd := exec.Command(bash, filepath.ToSlash(filepath.Join(scripts, script)))
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), append([]string{"NO_PUSH=1", "GITHUB_SHA="}, env...)...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	return repo, sh
}

func TestTagRelease_TagsTheVersionOnce(t *testing.T) {
	repo, sh := releaseRepo(t, "1.4.0")
	git := realGitForTest(t)

	out, err := sh("tag-release.sh")
	if err != nil || !strings.HasPrefix(out, "tagged v1.4.0") {
		t.Fatalf("first run = %q, %v; want it to tag v1.4.0", out, err)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "v1.4.0^{commit}"); got != gitOutput(t, git, repo, "rev-parse", "HEAD") {
		t.Fatalf("v1.4.0 points at %s, want HEAD", got)
	}
	out, err = sh("tag-release.sh")
	if err != nil || out != "[skip] v1.4.0 already exists" {
		t.Fatalf("second run = %q, %v; want the [skip] line", out, err)
	}
}

func TestTagRelease_RefusesAVersionThatIsNotSemver(t *testing.T) {
	_, sh := releaseRepo(t, "1.4")
	if out, err := sh("tag-release.sh"); err == nil || !strings.Contains(out, "not MAJOR.MINOR.PATCH") {
		t.Fatalf("run = %q, %v; want a refusal naming MAJOR.MINOR.PATCH", out, err)
	}
}

func TestNewestTag_PrintsTheHighestSemverNotTheLatestCreated(t *testing.T) {
	repo, sh := releaseRepo(t, "1.0.0")
	git := realGitForTest(t)
	for _, tag := range []string{"v1.10.0", "v1.9.0", "salvage/x", "v2.0.0-rc1"} {
		gitOutput(t, git, repo, "tag", tag)
	}
	if out, err := sh("newest-tag.sh"); err != nil || out != "v1.10.0" {
		t.Fatalf("newest = %q, %v; want v1.10.0", out, err)
	}
}

func TestNewestTag_FailsWithoutAReleaseTag(t *testing.T) {
	_, sh := releaseRepo(t, "1.0.0")
	if out, err := sh("newest-tag.sh"); err == nil || !strings.Contains(out, "no release tag") {
		t.Fatalf("run = %q, %v; want a failure naming 'no release tag'", out, err)
	}
}
