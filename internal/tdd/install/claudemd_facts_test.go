package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #889: the block asserted that `git` and `cargo` resolve to the queue
// shim on a box where install had put no shim on the agent's PATH, called
// that shim the primary checkout's WALL, and taught a TypeScript and Python
// repo to iterate with `cargo check`. Every line it renders is a fact about
// the repo and the install it was written by.

// ratchet: test_removed TestClaudeMDBlock_StatesTheQueueShimsOnlyWhenTheyAreOnTheAgentPath: the block states the shims as a condition on every box; TestClaudeMDBlock_StatesTheQueueShimsAsAConditionNeverAFact replaces it
// ratchet: test_removed TestQueueShimsOnAgentPath_OnlyAFirstEntryHoldingTheShimsCounts: QueueShimsOnAgentPath is deleted, since the block no longer reads the agent's PATH
// ratchet: test_removed TestQueueShimsOnAgentPath_TheProjectSettingsDecide: QueueShimsOnAgentPath is deleted, since the block no longer reads the agent's PATH
// ratchet: test_removed TestQueueShimsOnAgentPath_ProjectSettingsWithoutAPathLeaveTheUserOne: QueueShimsOnAgentPath is deleted, since the block no longer reads the agent's PATH
// ratchet: test_removed TestQueueShimsOnAgentPath_NoDirReadsNoSettings: QueueShimsOnAgentPath is deleted, since the block no longer reads the agent's PATH
// The shims are a fact about the box, and the block is committed and
// rendered by every box, so it states them as a condition that holds
// everywhere and never claims they are on PATH.
func TestClaudeMDBlock_StatesTheQueueShimsAsAConditionNeverAFact(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{})
	for _, want := range []string{
		"Where `aphrollo install` put the queue shims on the agent's PATH",
		"`aphrollo gate doctor` says whether it did",
		"where it is on the agent's PATH, is the WALL",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not say %q", want)
		}
	}
}

func TestClaudeMDBlock_RustLinesOnlyInACargoRepo(t *testing.T) {
	t.Parallel()
	other := ClaudeMDBlock(BlockFlags{Go: true, Npm: true})
	for _, word := range []string{"cargo", "clippy", "crate"} {
		if strings.Contains(other, word) {
			t.Errorf("a repo with no Cargo manifest is told about %q", word)
		}
	}
	rust := ClaudeMDBlock(BlockFlags{Cargo: true})
	for _, want := range []string{"cargo check -p <crate> --tests", "clippy", "`git` and `cargo` resolve to them"} {
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
