package cli

import (
	"strings"
	"testing"
)

// argvFromBlob turns one fuzzed string into an argv slice: fields separated
// by "\x1f" (a byte no real argv token contains), so the fuzzer can explore
// arg COUNT and boundaries, not just single-token content.
func argvFromBlob(blob string) []string {
	if blob == "" {
		return nil
	}
	return strings.Split(blob, "\x1f")
}

// FuzzGitShimArgv feeds an arbitrary argv (as the shim receives it from
// os.Args) to the git shim's pure, no-I/O argv classifiers. The shim sits in
// front of every git invocation on the box, so a crafted argv — an empty
// token, a "-C" with nothing after it, a "worktree" with no sub-verb — must
// classify cleanly rather than panic before the shim ever execs real git.
func FuzzGitShimArgv(f *testing.F) {
	seeds := []string{
		"status",
		"commit\x1f-m\x1fx",
		"-C\x1f/repo\x1fstatus",
		"-c\x1fuser.name=x\x1fcommit",
		"--git-dir",
		"worktree",
		"worktree\x1fadd\x1f-b\x1flane/x\x1f/path",
		"restore\x1f--staged",
		"apply\x1f--index",
		"branch\x1f-D\x1flane/x",
		"",
		"-C",
		"merge",
		"merge\x1f--abort",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, blob string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("git shim argv classifiers panicked on argv=%q: %v", argvFromBlob(blob), r)
			}
		}()
		args := argvFromBlob(blob)
		_, rest := gitGlobalArgs(args)
		_ = gitLockScopeFor(rest)
		_ = isPlainMerge(rest)
		_ = containsToken(args, "--staged")
	})
}
