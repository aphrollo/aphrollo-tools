package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseRepo is a repo with one commit, and the bash that runs deploy/*.sh
// against it (the test skips where there is no bash). stubs are scripts placed
// on PATH ahead of everything: the script under test calls aphrollo through
// $APHROLLO and gh through PATH, and neither is the real one here.
func releaseRepo(t *testing.T) (repo string, sh func(script string, env ...string) (string, error)) {
	t.Helper()
	isolateGitConfigCLI(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH") // skip-ok: the scripts under test are bash
	}
	git := realGitForTest(t)
	repo = t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "README.md"), "x\n")
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

// stubScript writes an executable bash script into dir and returns its slash path.
func stubScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	mustWriteFile(t, path, "#!/usr/bin/env bash\n"+body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

// planStub is an aphrollo whose `release plan` prints what a test says the plan is.
func planStub(t *testing.T, plan string) string {
	t.Helper()
	body := "if [ \"$1 $2\" = \"release plan\" ]; then [ -n \"" + plan + "\" ] && echo \"" + plan + "\"; exit 0; fi\nexit 9\n"
	return stubScript(t, t.TempDir(), "aphrollo", body)
}

func TestTagRelease_TagsWhatThePlanSaysOnceAtTheCommitItWasAskedFor(t *testing.T) {
	repo, sh := releaseRepo(t)
	git := realGitForTest(t)
	head := gitOutput(t, git, repo, "rev-parse", "HEAD")
	stub := planStub(t, "v1.7.0")

	out, err := sh("tag-release.sh", "APHROLLO="+stub, "NO_RELEASE=1")
	if err != nil || !strings.HasPrefix(out, "tagged v1.7.0") {
		t.Fatalf("first run = %q, %v; want it to tag v1.7.0", out, err)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "v1.7.0^{commit}"); got != head {
		t.Fatalf("v1.7.0 points at %s, want %s", got, head)
	}
	out, err = sh("tag-release.sh", "APHROLLO="+stub, "NO_RELEASE=1")
	if err != nil || !strings.Contains(out, "[skip] v1.7.0 already exists") {
		t.Fatalf("second run = %q, %v; want the [skip] line", out, err)
	}
}

func TestTagRelease_AnEmptyPlanMakesNoTag(t *testing.T) {
	repo, sh := releaseRepo(t)
	git := realGitForTest(t)

	out, err := sh("tag-release.sh", "APHROLLO="+planStub(t, ""), "NO_RELEASE=1")
	if err != nil || !strings.Contains(out, "[skip] no release") {
		t.Fatalf("run = %q, %v; want the [skip] line", out, err)
	}
	if tags := gitOutput(t, git, repo, "tag", "--list"); tags != "" {
		t.Fatalf("tags = %q, want none", tags)
	}
}

func TestTagRelease_APlanThatFailsFailsTheJob(t *testing.T) {
	_, sh := releaseRepo(t)
	stub := stubScript(t, t.TempDir(), "aphrollo", "echo 'aphrollo release plan: no release tag' >&2\nexit 1\n")

	if out, err := sh("tag-release.sh", "APHROLLO="+stub, "NO_RELEASE=1"); err == nil || !strings.Contains(out, "no release tag") {
		t.Fatalf("run = %q, %v; want the job to fail with the plan's own words", out, err)
	}
}

func TestTagRelease_RefusesAPlanThatIsNotAReleaseTag(t *testing.T) {
	_, sh := releaseRepo(t)
	if out, err := sh("tag-release.sh", "APHROLLO="+planStub(t, "main"), "NO_RELEASE=1"); err == nil || !strings.Contains(out, "not v<MAJOR.MINOR.PATCH>") {
		t.Fatalf("run = %q, %v; want a refusal naming the shape", out, err)
	}
}

// ghStub is a gh that logs every call, answers `release view` with the exit
// status and stderr text the test says, records the notes file a `release
// create` was given and answers it with createExit.
func ghStub(t *testing.T, viewExit, viewSaid, createExit string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "gh.log")
	stubScript(t, dir, "gh", "echo \"gh $*\" >> \""+filepath.ToSlash(log)+"\"\n"+
		"if [ \"$1 $2\" = \"release view\" ]; then echo '"+viewSaid+"' >&2; exit "+viewExit+"; fi\n"+
		"if [ \"$1 $2\" = \"release create\" ]; then while [ $# -gt 0 ]; do if [ \"$1\" = \"--notes-file\" ]; then cat \"$2\" >> \""+filepath.ToSlash(log)+"\"; fi; shift; done; exit "+createExit+"; fi\n")
	return dir, log
}

// ghRelease runs the release step for a repo whose newest tag is v1.7.0 and
// that has no new fragment, against a gh stubbed as given.
func ghRelease(t *testing.T, viewExit, viewSaid, createExit string) (out, logged string, err error) {
	t.Helper()
	repo, sh := releaseRepo(t)
	gitOutput(t, realGitForTest(t), repo, "tag", "v1.7.0")
	aphrollo := stubScript(t, t.TempDir(), "aphrollo", "if [ \"$1 $2\" = \"release plan\" ]; then exit 0; fi\nif [ \"$1 $2 $3\" = \"changelog --tag v1.7.0\" ]; then echo 'Notes of 1.7.0.'; exit 0; fi\nexit 9\n")
	ghDir, log := ghStub(t, viewExit, viewSaid, createExit)
	out, err = sh("tag-release.sh", "APHROLLO="+aphrollo, "PATH="+ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	data, _ := os.ReadFile(log)
	return out, string(data), err
}

// The tag can land and the Release fail; the next run finds no new tag to make
// and still gives the newest tag the Release it lacks, with its own notes.
func TestTagRelease_GivesTheNewestTagAReleaseWhenGhSaysItIsNotFound(t *testing.T) {
	out, logged, err := ghRelease(t, "1", "release not found", "0")
	if err != nil {
		t.Fatalf("run = %q, %v", out, err)
	}
	for _, want := range []string{"gh release view v1.7.0", "gh release create v1.7.0 --verify-tag --title v1.7.0 --notes-file", "Notes of 1.7.0."} {
		if !strings.Contains(logged, want) {
			t.Errorf("gh log lacks %q:\n%s", want, logged)
		}
	}
}

// A transient API error is not "no release": creating then would fail with
// "already exists". The job goes on, with a warning, to finish the release.
func TestTagRelease_AGhErrorOtherThanNotFoundCreatesNothingAndDoesNotFailTheJob(t *testing.T) {
	out, logged, err := ghRelease(t, "1", "HTTP 502: Bad Gateway", "0")
	if err != nil || !strings.Contains(out, "::warning::") {
		t.Fatalf("run = %q, %v; want exit 0 with a warning", out, err)
	}
	if strings.Contains(logged, "release create") {
		t.Fatalf("a release was created after a view error that was not 'not found':\n%s", logged)
	}
}

// The tag is pushed before the Release: the job must still finish.
func TestTagRelease_AFailedReleaseCreateDoesNotFailTheJob(t *testing.T) {
	out, _, err := ghRelease(t, "1", "release not found", "1")
	if err != nil || !strings.Contains(out, "::warning::") {
		t.Fatalf("run = %q, %v; want exit 0 with a warning", out, err)
	}
}

func TestTagRelease_LeavesAReleaseThatExistsAlone(t *testing.T) {
	out, logged, err := ghRelease(t, "0", "", "0")
	if err != nil || !strings.Contains(out, "[skip] release v1.7.0 already exists") {
		t.Fatalf("run = %q, %v", out, err)
	}
	if strings.Contains(logged, "release create") {
		t.Fatalf("a release that exists was created again:\n%s", logged)
	}
}

func TestNewestTag_PrintsTheHighestSemverNotTheLatestCreated(t *testing.T) {
	repo, sh := releaseRepo(t)
	git := realGitForTest(t)
	for _, tag := range []string{"v1.10.0", "v1.9.0", "salvage/x", "v2.0.0-rc1"} {
		gitOutput(t, git, repo, "tag", tag)
	}
	if out, err := sh("newest-tag.sh"); err != nil || out != "v1.10.0" {
		t.Fatalf("newest = %q, %v; want v1.10.0", out, err)
	}
}

func TestNewestTag_FailsWithoutAReleaseTag(t *testing.T) {
	_, sh := releaseRepo(t)
	if out, err := sh("newest-tag.sh"); err == nil || !strings.Contains(out, "no release tag") {
		t.Fatalf("run = %q, %v; want a failure naming 'no release tag'", out, err)
	}
}

// ratchet: test_removed TestTagRelease_TagsTheVersionOnce: the tag is no longer read from a VERSION file; the script tags what `aphrollo release plan` says (TestTagRelease_TagsWhatThePlanSaysOnceAtTheCommitItWasAskedFor)
// ratchet: test_removed TestTagRelease_RefusesAVersionThatIsNotSemver: there is no VERSION file to refuse; a plan that is not a release tag is refused (TestTagRelease_RefusesAPlanThatIsNotAReleaseTag)
// ratchet: test_removed TestTagRelease_GivesTheNewestTagAReleaseWhenItHasNone: renamed TestTagRelease_GivesTheNewestTagAReleaseWhenGhSaysItIsNotFound
