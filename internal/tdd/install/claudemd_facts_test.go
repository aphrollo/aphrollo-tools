package install

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #889: the block asserted that `git` and `cargo` resolve to the queue
// shim on a box where install had put no shim on the agent's PATH, called
// that shim the primary checkout's WALL, and taught a TypeScript and Python
// repo to iterate with `cargo check`. Every line it renders is a fact about
// the repo and the install it was written by.

func TestClaudeMDBlock_StatesTheQueueShimsOnlyWhenTheyAreOnTheAgentPath(t *testing.T) {
	t.Parallel()
	without := ClaudeMDBlock(BlockFlags{})
	with := ClaudeMDBlock(BlockFlags{QueueShims: true})
	for _, phrase := range []string{"resolves to the queue shim", "is the WALL"} {
		if strings.Contains(without, phrase) {
			t.Errorf("with no shim on the agent's PATH the block still says %q", phrase)
		}
		if !strings.Contains(with, phrase) {
			t.Errorf("with the shims on the agent's PATH the block does not say %q", phrase)
		}
	}
}

func TestClaudeMDBlock_RustLinesOnlyInACargoRepo(t *testing.T) {
	t.Parallel()
	other := ClaudeMDBlock(BlockFlags{QueueShims: true, Go: true, Npm: true})
	for _, word := range []string{"cargo", "clippy", "crate"} {
		if strings.Contains(other, word) {
			t.Errorf("a repo with no Cargo manifest is told about %q", word)
		}
	}
	rust := ClaudeMDBlock(BlockFlags{QueueShims: true, Cargo: true})
	for _, want := range []string{"cargo check -p <crate> --tests", "clippy", "`git` and `cargo` resolve to the queue shim"} {
		if !strings.Contains(rust, want) {
			t.Errorf("a cargo repo's block does not say %q", want)
		}
	}
}

func TestClaudeMDBlock_GoAndNpmLinesFollowTheRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		flags BlockFlags
		lines []string
	}{
		{BlockFlags{Go: true}, []string{"`go vet ./...`", "a Go root runs vet→lint→fail-first"}},
		{BlockFlags{Npm: true}, []string{"`npx tsc --noEmit`", "an npm root runs tsc→eslint→fail-first"}},
	}
	bare := ClaudeMDBlock(BlockFlags{})
	for _, generic := range []string{"Iterate with a compile-only command", "per root, the toolchain's own checks, then fail-first"} {
		if !strings.Contains(bare, generic) {
			t.Errorf("a repo with no toolchain the gate knows is not told %q", generic)
		}
	}
	for _, c := range cases {
		if block := ClaudeMDBlock(c.flags); !containsAll(block, c.lines) {
			t.Errorf("flags %+v: the block does not carry %q", c.flags, c.lines)
		}
		if block := ClaudeMDBlock(BlockFlags{}); containsAny(block, c.lines) {
			t.Errorf("a repo with neither toolchain is told %q", c.lines)
		}
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// The toolchains come from the repo's own manifests, at any depth: a web
// frontend in a subdirectory is as much the repo's as a root go.mod.
func TestBlockFlagsFor_DetectsTheRepoToolchains(t *testing.T) {
	bare := t.TempDir()
	gitInit(t, bare)
	if f := blockFlagsFor(bare); f.Cargo || f.Go || f.Npm {
		t.Errorf("a repo with no manifest detected %+v", f)
	}

	cases := map[string]func(BlockFlags) bool{
		"crates/core/Cargo.toml": func(f BlockFlags) bool { return f.Cargo && !f.Go && !f.Npm },
		"go.mod":                 func(f BlockFlags) bool { return f.Go && !f.Cargo && !f.Npm },
		"web/package.json":       func(f BlockFlags) bool { return f.Npm && !f.Cargo && !f.Go },
	}
	for manifest, ok := range cases {
		repo := t.TempDir()
		gitInit(t, repo)
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(manifest)), "{}\n")
		if f := blockFlagsFor(repo); !ok(f) {
			t.Errorf("%s detected %+v", manifest, f)
		}
	}
}

// writeAgentPath gives dir a settings.json whose env.PATH is dirs.
func writeAgentPath(t *testing.T, dir string, dirs ...string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"env": map[string]any{"PATH": strings.Join(dirs, envPathSep(hookGOOSFn()))}})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "settings.json"), string(data))
}

// installedShimDir is a dir holding the git and cargo queue shims.
func installedShimDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cargo-queue")
	bin := filepath.Join(t.TempDir(), "aphrollo")
	if _, err := InstallCargoShim(dir, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGitShim(dir, bin); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestQueueShimsOnAgentPath_OnlyAFirstEntryHoldingTheShimsCounts(t *testing.T) {
	t.Parallel()
	shims := installedShimDir(t)
	other := t.TempDir()

	first := t.TempDir()
	writeAgentPath(t, first, shims, other)
	if !QueueShimsOnAgentPath("", first) {
		t.Error("env.PATH starting at an installed shim dir must count")
	}

	behind := t.TempDir()
	writeAgentPath(t, behind, other, shims)
	if QueueShimsOnAgentPath("", behind) {
		t.Error("a shim dir behind another PATH entry does not decide what `git` resolves to")
	}

	empty := t.TempDir()
	writeAgentPath(t, empty, t.TempDir(), other)
	if QueueShimsOnAgentPath("", empty) {
		t.Error("a first PATH entry holding no shims must not count")
	}

	if QueueShimsOnAgentPath("", t.TempDir()) {
		t.Error("a config dir with no settings.json has no agent PATH")
	}
}

// A project's own settings.json sets the env.PATH its sessions get, over the
// user-level one, in both directions.
func TestQueueShimsOnAgentPath_TheProjectSettingsDecide(t *testing.T) {
	shims := installedShimDir(t)
	withShims, withoutShims := t.TempDir(), t.TempDir()
	writeAgentPath(t, withShims, shims)
	writeAgentPath(t, withoutShims, t.TempDir())

	repo := t.TempDir()
	gitInit(t, repo)
	writeAgentPath(t, filepath.Join(repo, ".claude"), shims)
	if !QueueShimsOnAgentPath(repo, withoutShims) {
		t.Error("the project's env.PATH starts at the shims, so its sessions resolve git there")
	}

	writeAgentPath(t, filepath.Join(repo, ".claude"), t.TempDir())
	if QueueShimsOnAgentPath(repo, withShims) {
		t.Error("the project's env.PATH replaces the user-level one, and it holds no shims")
	}
}

// Project settings that set no env.PATH leave the user-level one in force.
func TestQueueShimsOnAgentPath_ProjectSettingsWithoutAPathLeaveTheUserOne(t *testing.T) {
	user := t.TempDir()
	writeAgentPath(t, user, installedShimDir(t))
	repo := t.TempDir()
	gitInit(t, repo)
	mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), "{}\n")

	if !QueueShimsOnAgentPath(repo, user) {
		t.Error("the project sets no env.PATH, so the user-level one, starting at the shims, is the session's")
	}
}

// With no repo and no config dir there are no settings to read: the process's
// working directory is not a config dir.
func TestQueueShimsOnAgentPath_NoDirReadsNoSettings(t *testing.T) {
	cwd := t.TempDir()
	writeAgentPath(t, cwd, installedShimDir(t))
	t.Chdir(cwd)

	if QueueShimsOnAgentPath("", "") {
		t.Error("the working directory's settings.json was read as a config dir's")
	}
}
