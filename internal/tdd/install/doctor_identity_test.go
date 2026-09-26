package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// identityInstall is a healthy install judged from an undercover (or not)
// git repo, with git's global identity set to name and email in an isolated
// global config.
func identityInstall(t *testing.T, on bool, name, email string) DoctorInput {
	t.Helper()
	in := healthyInstall(t)
	repo := t.TempDir()
	// gitInit isolates the global config itself, so it runs before the
	// identity is written there.
	gitInit(t, repo)
	for key, val := range map[string]string{"user.name": name, "user.email": email} {
		if out, err := exec.Command(gitBinary(), "config", "--global", key, val).CombinedOutput(); err != nil {
			t.Fatalf("git config --global %s: %v\n%s", key, err, out)
		}
	}
	manifest := "[aphrollo]\n"
	if on {
		manifest += "undercover = true\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	in.Repo = repo
	return in
}

// The session that pushed a tell branch started with git's global identity
// set to the tool's own; doctor says so before the first commit does.
func TestDoctor_FailsAToolGlobalIdentityInAnUndercoverRepo(t *testing.T) {
	in := identityInstall(t, true, "Claude", "noreply@anthropic.com")
	c := check(t, Doctor(in), "git identity")
	if c.OK {
		t.Fatal("a tool identity must fail the check")
	}
	for _, want := range []string{"Claude <noreply@anthropic.com>", "git config --global user.name", "git config --global user.email"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q must name %q", c.Detail, want)
		}
	}
}

func TestDoctor_AcceptsAPersonsGlobalIdentity(t *testing.T) {
	in := identityInstall(t, true, "Claudia Airey", "claudia@example.com")
	if c := check(t, Doctor(in), "git identity"); !c.OK {
		t.Fatalf("an ordinary identity failed: %s", c.Detail)
	}
}

func TestDoctor_SkipsTheIdentityCheckWhenTheRepoNeverAsked(t *testing.T) {
	in := identityInstall(t, false, "Claude", "noreply@anthropic.com")
	for _, c := range Doctor(in) {
		if c.Name == "git identity" {
			t.Fatalf("a repo that never opted in was judged on its identity: %+v", c)
		}
	}
}

// A repo-level identity signs every commit in that repo just the same.
func TestDoctor_FailsAToolRepoIdentityUnderAPersonsGlobalOne(t *testing.T) {
	in := identityInstall(t, true, "Jane Doe", "jane@example.com")
	gitDo(t, in.Repo, "config", "user.name", "Claude")
	gitDo(t, in.Repo, "config", "user.email", "noreply@anthropic.com")
	c := check(t, Doctor(in), "git identity")
	if c.OK {
		t.Fatal("a tool identity in the repo config must fail the check")
	}
	for _, want := range []string{"repo", "Claude <noreply@anthropic.com>", "git config user.name"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q must name %q", c.Detail, want)
		}
	}
}
