package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// aphrollo update builds from origin/main in a detached temporary worktree
// rather than the working tree self-install uses, because the box running
// it is not guaranteed to be sitting in a clean checkout at that commit.
// These pin: the skip when already there, that the build genuinely never
// touches the working tree, that the temporary worktree is always cleaned
// up, and that the swap/sweep behaves exactly like self-install's.

// updateFixture lays out a bare origin and a clone of it (the --repo under
// test), plus the seed checkout the clone came from -- kept around so a test
// can push origin further ahead of the clone without disturbing it.
func updateFixture(t *testing.T) (origin, clone, seed string) {
	t.Helper()
	isolateGitConfigCLI(t)
	git := realGitForTest(t)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	origin = filepath.Join(t.TempDir(), "origin")
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)

	seed = t.TempDir()
	run(seed, "init", "-q")
	run(seed, "config", "user.email", "t@example.com")
	run(seed, "config", "user.name", "t")
	run(seed, "checkout", "-q", "-B", "main")
	mustWriteFile(t, filepath.Join(seed, "go.mod"), "module github.com/aphrollo/aphrollo-tools\n\ngo 1.26.6\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "init")
	run(seed, "remote", "add", "origin", origin)
	run(seed, "push", "-q", "origin", "main")

	clone = filepath.Join(t.TempDir(), "clone")
	run(t.TempDir(), "clone", "-q", origin, clone)
	run(clone, "config", "user.email", "t@example.com")
	run(clone, "config", "user.name", "t")
	return origin, clone, seed
}

func gitOutput(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestUpdate_SkipsWhenTheBinaryIsAlreadyAtOriginMain(t *testing.T) {
	origin, clone, _ := updateFixture(t)
	git := realGitForTest(t)
	head := gitOutput(t, git, origin, "rev-parse", "main")
	buildinfo.SetForTest(head, "2026-01-01T00:00:00Z")
	t.Cleanup(func() { buildinfo.SetForTest("", "") })

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
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	want := fmt.Sprintf("aphrollo update: already at %s [skip]\n", head[:7])
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
	if called {
		t.Fatal("the build seam must not run when already at origin/main")
	}
	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "OLD" {
		t.Fatalf("the binary changed on a skip, got %q (%v)", got, err)
	}
}

func TestUpdate_BuildsFromADetachedWorktreeAtOriginMainNotTheWorkingTree(t *testing.T) {
	_, clone, seed := updateFixture(t)
	git := realGitForTest(t)

	// origin moves ahead of the clone, which the update must still reach.
	if err := os.WriteFile(filepath.Join(seed, "second.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "second")
	run(seed, "push", "-q", "origin", "main")
	wantHead := gitOutput(t, git, seed, "rev-parse", "main")

	// the clone has an uncommitted edit of its own, which the build must
	// leave alone: it never runs against this working tree.
	dirty := "module github.com/aphrollo/aphrollo-tools\n\ngo 1.26.6\n// dirty edit\n"
	if err := os.WriteFile(filepath.Join(clone, "go.mod"), []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}

	var recordedRepo, recordedHead, recordedStatus string
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		recordedRepo = repo
		recordedHead = gitOutput(t, git, repo, "rev-parse", "HEAD")
		recordedStatus = gitOutput(t, git, repo, "status", "--porcelain")
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if recordedRepo == clone || recordedRepo == "" {
		t.Fatalf("build ran against %q, want a detached worktree distinct from the clone %q", recordedRepo, clone)
	}
	if recordedHead != wantHead {
		t.Fatalf("worktree HEAD = %s, want origin/main %s", recordedHead, wantHead)
	}
	if recordedStatus != "" {
		t.Fatalf("worktree was dirty at build time: %q", recordedStatus)
	}
	stillDirty, err := os.ReadFile(filepath.Join(clone, "go.mod"))
	if err != nil || !strings.Contains(string(stillDirty), "// dirty edit") {
		t.Fatalf("the clone's own uncommitted edit was disturbed, got %q (%v)", stillDirty, err)
	}
}

func TestUpdate_RemovesTheTemporaryWorktreeEvenWhenTheBuildFails(t *testing.T) {
	_, clone, _ := updateFixture(t)
	git := realGitForTest(t)

	var recordedRepo string
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		recordedRepo = repo
		return "go build", errors.New("cmd/aphrollo does not compile")
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code == 0 {
		t.Fatal("a failed build must not report success")
	}
	if recordedRepo == "" {
		t.Fatal("the build seam was never called")
	}
	if _, err := os.Stat(recordedRepo); !os.IsNotExist(err) {
		t.Fatalf("the temporary worktree must be removed after a failed build, stat err = %v", err)
	}
	listing := gitOutput(t, git, clone, "worktree", "list")
	lines := strings.Split(listing, "\n")
	if len(lines) != 1 {
		t.Fatalf("git worktree list = %q, want only the clone left registered", listing)
	}
	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "OLD" {
		t.Fatalf("the binary changed on a failed build, got %q (%v)", got, err)
	}
}

func TestUpdate_SwapsAndSweepsLikeSelfInstall(t *testing.T) {
	_, clone, _ := updateFixture(t)

	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	dir := t.TempDir()
	bin := filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleOld := filepath.Join(dir, "aphrollo.stale-1700000000.exe")
	if err := os.WriteFile(staleOld, []byte("OLDER"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr: %s", code, errb.String())
	}

	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "NEW" {
		t.Fatalf("bin = %q (%v), want the seam's fixed output", got, err)
	}
	if _, err := os.Stat(staleOld); err == nil {
		t.Fatal("the pre-existing stale copy must be swept")
	}
	stale := staleCopies(t, dir)
	if len(stale) != 1 {
		t.Fatalf("want exactly one new stale copy from this run, got %v", stale)
	}
	if body, err := os.ReadFile(stale[0]); err != nil || string(body) != "OLD" {
		t.Fatalf("the stale copy must hold the replaced binary, got %q (%v)", body, err)
	}
	// One line per step, so an operator can see which one failed, each
	// prefixed the same way self-install's are.
	for _, want := range []string{"build", "rename", "move", "sweep"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not report the %q step:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "aphrollo update") {
			t.Errorf("line %q does not carry the %q prefix", line, "aphrollo update")
		}
	}
}

// #366: an explicit --bin with no extension on Windows must still land the
// build somewhere exec.LookPath (and everything Go spawns) can find it, not
// only a human's shell — the same fix self-install got, shared.
func TestUpdate_NormalizesAnExtensionlessBinFlagToExe(t *testing.T) {
	pinBinGOOS(t, "windows")
	_, clone, _ := updateFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	dir := t.TempDir()
	bin := filepath.Join(dir, "aphrollo") // deliberately no extension

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	wantBin := bin + ".exe"
	got, err := os.ReadFile(wantBin)
	if err != nil {
		t.Fatalf("%s does not exist after update: %v", wantBin, err)
	}
	if string(got) != "NEW" {
		t.Fatalf("%s content = %q, want the freshly built one", wantBin, got)
	}
	if _, err := os.Stat(bin); err == nil {
		t.Fatalf("an extensionless %s must not exist beside %s", bin, wantBin)
	}
}

// #366: the outage traced to this exact step — with the extension dropped,
// swapBinary stat'd the wrong name, found nothing, and swept the good
// binary instead of renaming it aside. This pins that once bin is
// normalized, the pre-existing .exe IS found and preserved.
func TestUpdate_RenamesThePreExistingExeAsideWhenBinFlagOmitsTheExtension(t *testing.T) {
	pinBinGOOS(t, "windows")
	_, clone, _ := updateFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	dir := t.TempDir()
	exe := filepath.Join(dir, "aphrollo.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "aphrollo") // the flag as a caller who forgot the extension would pass it

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "rename skipped") {
		t.Fatalf("the pre-existing binary was not found under its real name, output:\n%s", out.String())
	}
	stale := staleCopies(t, dir)
	if len(stale) != 1 {
		t.Fatalf("the replaced binary must be renamed aside, found %v", stale)
	}
	if body, err := os.ReadFile(stale[0]); err != nil || string(body) != "OLD" {
		t.Fatalf("the stale copy must hold the previous binary, got %q (%v)", body, err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "NEW" {
		t.Fatalf("%s = %q (%v), want the freshly built bytes", exe, got, err)
	}
}

func TestUpdate_RepoHelpDescribesFetchNotBuild(t *testing.T) {
	var out, errb bytes.Buffer
	runUpdate([]string{"--bogus"}, &out, &errb)

	want := "aphrollo-tools checkout whose remote to fetch from (the build runs in a temporary worktree)"
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("stderr = %q, want it to contain %q", errb.String(), want)
	}
	if strings.Contains(errb.String(), "module to build") {
		t.Fatalf("stderr = %q, still carries self-install's stale wording", errb.String())
	}
}

func TestUpdate_AcceptsAGoModWithALeadingComment(t *testing.T) {
	_, clone, _ := updateFixture(t)
	git := realGitForTest(t)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustWriteFile(t, filepath.Join(clone, "go.mod"), "// first-party tooling\n\nmodule github.com/aphrollo/aphrollo-tools\n\ngo 1.26.6\n")
	run(clone, "add", "-A")
	run(clone, "commit", "-q", "-m", "leading comment in go.mod")

	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb)
	if code == 2 {
		t.Fatalf("update rejected a go.mod with a leading comment: stderr = %q", errb.String())
	}
}

func TestUpdate_RefusesARepoThatIsNotAphrolloTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n\ngo 1.26.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", dir, "--no-init"}, &out, &errb)
	if code != 2 {
		t.Fatalf("update exit = %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "--repo") {
		t.Fatalf("stderr does not name --repo: %q", errb.String())
	}
}
