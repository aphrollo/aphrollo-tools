package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// installMode is one way `aphrollo install` is run on a real box: with the
// default config dir ($CLAUDE_CONFIG_DIR), with --no-git on a shared box where
// a global core.hooksPath would gate every other repo, and project-scoped with
// --config-dir <repo>/.claude (issue #886's repro, which is also --no-git).
type installMode struct {
	name        string
	noGit       bool
	projectDirs bool // --config-dir <repo>/.claude instead of $CLAUDE_CONFIG_DIR
}

var installModes = []installMode{
	{name: "default"},
	{name: "no-git", noGit: true},
	{name: "config-dir", noGit: true, projectDirs: true},
}

// installFixture is a repo that keeps a CLAUDE.md and a .ratchet dir, plus
// the scratch dirs one install run writes into.
type installFixture struct {
	repo, cfg, hooksDir, shimDir string
}

// runInstallMode installs into a fresh fixture repo the way mode says, and
// fails the test on a non-zero exit.
func runInstallMode(t *testing.T, mode installMode) installFixture {
	t.Helper()
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1") // --git-hooks-dir below sits under t.TempDir()
	f := installFixture{
		repo:     resolvedTempDir(t),
		hooksDir: filepath.Join(t.TempDir(), "githooks"),
		shimDir:  filepath.Join(t.TempDir(), "cargo-queue"),
	}
	gitInitRepo(t, f.repo)
	writeFile(t, filepath.Join(f.repo, "CLAUDE.md"), "# Project\n\nGuidance.\n")
	if err := os.MkdirAll(filepath.Join(f.repo, ".ratchet", "laws"), 0o755); err != nil {
		t.Fatal(err)
	}

	args := []string{"install", "--repo", f.repo, "--bin", fakeInstalledBin(t),
		"--git-hooks-dir", f.hooksDir, "--cargo-shim-dir", f.shimDir}
	if mode.projectDirs {
		f.cfg = filepath.Join(f.repo, ".claude")
		args = append(args, "--config-dir", f.cfg)
		gateConfigDir(t) // a user-level dir install must NOT write
	} else {
		f.cfg = gateConfigDir(t)
	}
	if mode.noGit {
		args = append(args, "--no-git")
	}

	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	return f
}

// Issue #886: --no-git returned before the repo's own files were written, so
// a repo that keeps a CLAUDE.md never got the managed block and nothing said
// so. --no-git skips the global git gate and the queue shims; the block and
// the law spec belong to the repo, and every mode writes them.
func TestInstall_EveryModeWritesTheBlockAndTheLawSpec(t *testing.T) {
	for _, mode := range installModes {
		t.Run(mode.name, func(t *testing.T) {
			f := runInstallMode(t, mode)
			if got := readFile(t, filepath.Join(f.repo, "CLAUDE.md")); !strings.Contains(got, "<!-- aphrollo:begin -->") {
				t.Errorf("install %s left CLAUDE.md without the managed block:\n%s", mode.name, got)
			}
			if _, err := os.Stat(filepath.Join(f.repo, ".ratchet", "README.md")); err != nil {
				t.Errorf("install %s did not write .ratchet/README.md: %v", mode.name, err)
			}
		})
	}
}

// The other half of --no-git's contract: the global git gate and the queue
// shims are exactly what it skips, and a default install still writes both.
func TestInstall_NoGitSkipsOnlyTheGlobalGateAndTheShims(t *testing.T) {
	for _, mode := range installModes {
		t.Run(mode.name, func(t *testing.T) {
			f := runInstallMode(t, mode)
			_, gateErr := os.Stat(filepath.Join(f.hooksDir, "pre-commit"))
			_, shimErr := os.Stat(f.shimDir)
			if mode.noGit {
				if gateErr == nil {
					t.Errorf("install %s wrote the global git gate in %s", mode.name, f.hooksDir)
				}
				if shimErr == nil {
					t.Errorf("install %s wrote the queue shims in %s", mode.name, f.shimDir)
				}
				return
			}
			if gateErr != nil {
				t.Errorf("install %s did not write the global git gate: %v", mode.name, gateErr)
			}
			if shimErr != nil {
				t.Errorf("install %s did not write the queue shims: %v", mode.name, shimErr)
			}
		})
	}
}

// Uninstall removes the gate; it never writes the block into a repo that
// does not carry one, whichever half --no-git leaves out.
func TestInstall_UninstallWritesNoBlock(t *testing.T) {
	for _, noGit := range []bool{false, true} {
		isolateGit(t)
		t.Setenv(tdd.HooksDirUnsafeEnv, "1")
		repo := resolvedTempDir(t)
		gitInitRepo(t, repo)
		writeFile(t, filepath.Join(repo, "CLAUDE.md"), "# Project\n")
		args := []string{"install", "--uninstall", "--repo", repo, "--config-dir", gateConfigDir(t),
			"--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
			"--cargo-shim-dir", filepath.Join(t.TempDir(), "cargo-queue")}
		if noGit {
			args = append(args, "--no-git")
		}
		var out, errb bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("uninstall (no-git=%v) exit = %d\n%s%s", noGit, code, out.String(), errb.String())
		}
		if got := readFile(t, filepath.Join(repo, "CLAUDE.md")); got != "# Project\n" {
			t.Errorf("uninstall (no-git=%v) rewrote CLAUDE.md:\n%s", noGit, got)
		}
	}
}

// Issue #887: a project-scoped install (--config-dir <repo>/.claude) puts the
// skills and agents where Claude Code loads a project's own, and doctor
// looked only in the user-level config dir, so it failed an install that was
// complete. Doctor, run with its defaults from the repo, accepts the managed
// files wherever an install mode puts them.
func TestDoctor_FindsTheManagedFilesEveryInstallModeWrote(t *testing.T) {
	for _, mode := range installModes {
		t.Run(mode.name, func(t *testing.T) {
			f := runInstallMode(t, mode)
			var found bool
			for _, c := range tdd.Doctor(doctorInput("", "", f.repo)) {
				if c.Name != "managed skills and agents" {
					continue
				}
				found = true
				if !c.OK {
					t.Errorf("after install %s, doctor fails the managed files: %s", mode.name, c.Detail)
				}
			}
			if !found {
				t.Fatal("doctor ran no managed skills and agents check")
			}
		})
	}
}

// Issue #889: the block states the queue shims only where install put them
// on the agent's env.PATH, which --no-git never does, and doctor judges the
// block against that same fact, so a fresh install of any mode is current.
func TestInstall_TheBlockStatesTheShimsOnlyWhereInstallPutThem(t *testing.T) {
	for _, mode := range installModes {
		t.Run(mode.name, func(t *testing.T) {
			f := runInstallMode(t, mode)
			states := strings.Contains(readFile(t, filepath.Join(f.repo, "CLAUDE.md")), "resolves to the queue shim")
			if states == mode.noGit {
				t.Errorf("install %s: block states the queue shims = %v, want %v", mode.name, states, !mode.noGit)
			}
			for _, c := range tdd.Doctor(doctorInput("", "", f.repo)) {
				if c.Name == "CLAUDE.md block" && !c.OK {
					t.Errorf("after install %s, doctor calls the block it wrote stale: %s", mode.name, c.Detail)
				}
			}
		})
	}
}
