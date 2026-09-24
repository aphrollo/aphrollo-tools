package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestMutantsStateDir_UnderTheConfiguredStateDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := mutantsStateDir()
	want := filepath.Join(cfg, "gate-state", "mutants")
	if dir != want {
		t.Fatalf("mutantsStateDir() = %q, want %q", dir, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("mutantsStateDir must create the directory: %v", err)
	}
}

func TestMutantsStateDir_EmptyWhenNoStateDirIsResolvable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if dir := mutantsStateDir(); dir != "" {
		t.Fatalf("mutantsStateDir() = %q, want \"\" with no resolvable home", dir)
	}
}

func TestMutantsLogDir_KeyedByProject(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a := mutantsLogDir("/repo/a")
	b := mutantsLogDir("/repo/b")
	if a == "" || b == "" {
		t.Fatalf("expected non-empty dirs, got %q and %q", a, b)
	}
	if a == b {
		t.Fatalf("two different repos must not share a mutants log dir: %q", a)
	}
	if got := mutantsLogDir("/repo/a"); got != a {
		t.Fatalf("mutantsLogDir must be stable for the same root: %q vs %q", got, a)
	}
}

func TestCommonGitDir_ResolvesTheSharedGitDirectory(t *testing.T) {
	root := tddtest.MakeGoRepo(t)
	got := commonGitDir(root)
	if got == "" {
		t.Fatal("commonGitDir on a real repo should not be empty")
	}
	if filepath.Base(got) != ".git" {
		t.Fatalf("commonGitDir = %q, want it to end in .git", got)
	}
}

func TestCommonGitDir_EmptyOutsideARepo(t *testing.T) {
	if got := commonGitDir(t.TempDir()); got != "" {
		t.Fatalf("commonGitDir outside a repo = %q, want \"\"", got)
	}
}

func TestPrimaryCheckoutRoot_ResolvesAWorktreeBackToItsPrimaryCheckout(t *testing.T) {
	primary := tddtest.MakeGoRepo(t)

	if got := primaryCheckoutRoot(primary); got != primary {
		t.Fatalf("primaryCheckoutRoot(primary) = %q, want %q", got, primary)
	}

	laneParent := t.TempDir()
	lane := filepath.Join(laneParent, "lane")
	tddtest.GitDo(t, primary, "worktree", "add", "-b", "lane/probe", lane, "HEAD")

	if got := primaryCheckoutRoot(lane); got != primary {
		t.Fatalf("primaryCheckoutRoot(lane) = %q, want the primary checkout %q", got, primary)
	}
}

func TestPrimaryCheckoutRoot_EmptyWhenCommonGitDirIsUnresolvable(t *testing.T) {
	if got := primaryCheckoutRoot(t.TempDir()); got != "" {
		t.Fatalf("primaryCheckoutRoot outside a repo = %q, want \"\"", got)
	}
}

func TestNormalizeRepoSpelling_ConvergesEquivalentSpellings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"backslashes to forward", `C:\repo\.git`, "C:/repo/.git"},
		{"trailing slash trimmed", "/repo/.git/", "/repo/.git"},
		{"bare repo root gets .git appended", "/repo", "/repo/.git"},
		{"already .git left alone", "/repo/.git", "/repo/.git"},
		{"bare \".git\" left alone", ".git", ".git"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeRepoSpelling(c.in); got != c.want {
				t.Fatalf("normalizeRepoSpelling(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSetPidRunningForTest_ReplacesAndRestoresTheSeam(t *testing.T) {
	original := pidRunningFn
	restore := SetPidRunningForTest(func(pid int) bool { return pid == 42 })

	if !pidRunningFn(42) || pidRunningFn(7) {
		t.Fatal("SetPidRunningForTest did not install the replacement")
	}

	restore()
	if reflectFnEqual := pidRunningFnIsOriginal(original); !reflectFnEqual {
		t.Fatal("restore() did not put the original probe back")
	}
}

// pidRunningFnIsOriginal exists only so the restore assertion above does not
// depend on comparing func values directly (which Go disallows except to
// nil): it re-runs the same probe on a known-live pid (this process) and
// checks it behaves like pidRunning, not like the swapped-in stub.
func pidRunningFnIsOriginal(original func(pid int) bool) bool {
	return pidRunningFn(os.Getpid()) == original(os.Getpid())
}

func TestAphrolloTomlFlag_ReadsTheAphrolloTableBool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\nundercover = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !aphrolloTomlFlag(root, "undercover") {
		t.Fatal("aphrolloTomlFlag should read the true set under [aphrollo]")
	}
	if aphrolloTomlFlag(root, "absent") {
		t.Fatal("an absent key must read as false")
	}
}

func TestAphrolloTomlString_ReadsTheAphrolloTableString(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\nname = \"borld\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, set := aphrolloTomlString(root, "name"); v != "borld" || !set {
		t.Fatalf("aphrolloTomlString = (%q, %v), want (borld, true)", v, set)
	}
	if v, set := aphrolloTomlString(root, "absent"); v != "" || set {
		t.Fatalf("absent key: (%q, %v), want (\"\", false)", v, set)
	}
}

func TestFileExists_TrueForARealPathFalseOtherwise(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "here")
	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(present) {
		t.Fatal("fileExists should be true for a file that is there")
	}
	if fileExists(filepath.Join(dir, "absent")) {
		t.Fatal("fileExists should be false for a path that is not there")
	}
}

func TestLogf_WritesALineAndNoOpsOnANilWriter(t *testing.T) {
	var buf bytes.Buffer
	logf(&buf, "run %d of %d", 1, 3)
	if buf.String() != "run 1 of 3\n" {
		t.Fatalf("logf wrote %q, want %q", buf.String(), "run 1 of 3\n")
	}

	// Must not panic when the caller kept no log.
	logf(nil, "ignored %d", 1)
}
