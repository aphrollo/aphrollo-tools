package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Issue #753, borld lane/slidenode-red: a measurement chain of the form
// `<edit> && cargo nextest run … && git checkout -- cell.rs && <next
// measurement>` had its checkout refused by the git shim — and the next
// measurement ran anyway, on the mutated tree, labelled as the restored one.
// The report asks for the one property a chain depends on: every refusal the
// shims print exits non-zero, so `&&` stops there, and prints the refusal on
// stderr, so a pipe on stdout never swallows it.
//
// These run the REAL binary, installed the two ways a box installs it: a copy
// named after the tool (the Windows queue dir's git.exe/cargo.exe, dispatched
// on argv[0]) and, off Windows, the sh script that execs `aphrollo gate git`.
// A refusal that returned 1 from runGitShim and was then lost between the
// dispatch and the process exit is exactly what a unit test of runGitShim
// cannot see.

var (
	aphrolloBinOnce sync.Once
	aphrolloBinPath string
	aphrolloBinErr  error
)

// builtAphrollo builds cmd/aphrollo once per test binary.
func builtAphrollo(t *testing.T) string {
	t.Helper()
	aphrolloBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aphrollo-shim-bin-")
		if err != nil {
			aphrolloBinErr = err
			return
		}
		registerStubDir(dir)
		out := filepath.Join(dir, "aphrollo"+exeSuffix())
		cmd := exec.Command("go", "build", "-o", out, "github.com/aphrollo/aphrollo-tools/cmd/aphrollo")
		if combined, err := cmd.CombinedOutput(); err != nil {
			aphrolloBinErr = fmt.Errorf("go build cmd/aphrollo: %v: %s", err, combined)
			return
		}
		aphrolloBinPath = out
	})
	if aphrolloBinErr != nil {
		t.Fatalf("could not build the aphrollo binary: %v", aphrolloBinErr)
	}
	return aphrolloBinPath
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// shimInstall is one way a queue dir puts the binary in front of a tool.
type shimInstall struct {
	name    string
	install func(t *testing.T, dir, bin, tool string)
}

func shimInstalls() []shimInstall {
	installs := []shimInstall{{"copy named after the tool", func(t *testing.T, dir, bin, tool string) {
		t.Helper()
		data, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tool+exeSuffix()), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}}}
	if runtime.GOOS != "windows" {
		installs = append(installs, shimInstall{"sh script", func(t *testing.T, dir, bin, tool string) {
			t.Helper()
			var err error
			if tool == "git" {
				_, err = tdd.InstallGitShim(dir, bin)
			} else {
				_, err = tdd.InstallCargoShim(dir, bin)
			}
			if err != nil {
				t.Fatal(err)
			}
		}})
	}
	return installs
}

// refusalRepos builds a primary checkout with one linked lane worktree, so
// both the primary's walls and a lane's discard walls stand. The lane carries
// an unstaged edit and an untracked file; branch x holds a commit nothing
// else has; main has moved on since the lane forked, adding a file the lane
// never touched.
func refusalRepos(t *testing.T) (primary, lane string) {
	t.Helper()
	isolateGit(t)
	primary = t.TempDir()
	lane = filepath.Join(t.TempDir(), "lane")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := fixtureGit(append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	put := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(primary, "init", "-q", "-b", "main")
	run(primary, "config", "user.email", "t@t")
	run(primary, "config", "user.name", "t")
	run(primary, "config", "commit.gpgsign", "false")
	put(filepath.Join(primary, "a.txt"), "one\n")
	put(filepath.Join(primary, "c.txt"), "base\n")
	run(primary, "add", ".")
	run(primary, "commit", "-qm", "base")
	run(primary, "branch", "x")
	run(primary, "worktree", "add", "-q", "-b", "lane/l", lane)
	run(lane, "checkout", "-q", "x")
	put(filepath.Join(lane, "x.txt"), "only on x\n")
	run(lane, "add", ".")
	run(lane, "commit", "-qm", "x only")
	run(lane, "checkout", "-q", "lane/l")
	// The lane and trunk both rewrite c.txt, so trunk does not merge into the
	// lane cleanly, and trunk alone adds m.txt: the stale-push shape.
	put(filepath.Join(lane, "c.txt"), "lane\n")
	run(lane, "commit", "-qam", "lane rewrites c")
	put(filepath.Join(primary, "c.txt"), "trunk\n")
	put(filepath.Join(primary, "m.txt"), "trunk moved\n")
	run(primary, "add", ".")
	run(primary, "commit", "-qm", "trunk moves")
	put(filepath.Join(lane, "a.txt"), "the lane's unstaged work\n")
	put(filepath.Join(lane, "u.txt"), "untracked\n")
	return primary, lane
}

// chainStops runs `<command> && echo CONTINUED` under sh with the shim dir
// first on PATH, and fails unless the refusal stopped the chain from stderr.
func chainStops(t *testing.T, dir, shimDir, command string, env ...string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command+" && echo CONTINUED")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("%q exited 0, so the && chain went on; stdout %q, stderr %q", command, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("%q wrote %q to stdout — the refusal belongs on stderr and nothing else ran", command, stdout.String())
	}
	if !strings.Contains(stderr.String(), "gate: ") {
		t.Errorf("%q printed no gate refusal on stderr: %q", command, stderr.String())
	}
}

func TestGitShim_EveryRefusalStopsAnAndChain(t *testing.T) {
	gateConfigDir(t)
	withDirectGitShim(t)
	t.Setenv("APHROLLO_PRIMARY_EDITS", "")
	t.Setenv("APHROLLO_DISCARD", "")
	t.Setenv("APHROLLO_DISCARD_UNSTAGED", "")
	t.Setenv("MUTATION", "")
	t.Setenv("MUTANT", "")
	t.Setenv("APHROLLO_REAL_GIT", realGitForTest(t))
	bin := builtAphrollo(t)
	primary, lane := refusalRepos(t)

	cases := []struct {
		name, dir, command string
		env                []string
	}{
		{"discard: checkout -- <path>", lane, "git checkout -- a.txt", nil},
		{"discard: restore <path>", lane, "git restore a.txt", nil},
		{"discard: reset --hard", lane, "git reset --hard", nil},
		{"discard: clean -fd", lane, "git clean -fd", nil},
		{"discard: branch -D unmerged", lane, "git branch -D x", nil},
		{"mutation restore with no hold", lane, "git checkout -- a.txt", []string{"MUTATION=1"}},
		{"unlabelled stash", lane, "git stash", nil},
		{"push deleting paths the lane never touched", lane, "git push origin lane/l", nil},
		{"primary: new branch", primary, "git checkout -b y", nil},
		{"primary: hooks bypass", primary, "git commit --allow-empty --no-verify -m m", nil},
	}
	for _, install := range shimInstalls() {
		shimDir := t.TempDir()
		install.install(t, shimDir, bin, "git")
		for _, c := range cases {
			t.Run(install.name+"/"+c.name, func(t *testing.T) {
				chainStops(t, c.dir, shimDir, c.command, c.env...)
			})
		}
	}
}

func TestCargoShim_EveryRefusalStopsAnAndChain(t *testing.T) {
	gateConfigDir(t)
	t.Setenv(tdd.BuildLockHeldEnv, "")
	t.Setenv(tdd.MutationGateEnv, "")
	// Never run: the refusal comes before the real cargo is resolved to a
	// process, and a path that does not exist proves it.
	t.Setenv("APHROLLO_REAL_CARGO", filepath.Join(t.TempDir(), "no-such-cargo"))
	bin := builtAphrollo(t)

	for _, install := range shimInstalls() {
		shimDir := t.TempDir()
		install.install(t, shimDir, bin, "cargo")
		t.Run(install.name+"/bare cargo mutants", func(t *testing.T) {
			chainStops(t, t.TempDir(), shimDir, "cargo mutants")
		})
	}
}
