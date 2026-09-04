package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This package's prepare step runs
//
//	git config --global --add safe.directory <dir>
//
// against the OPERATOR'S real global git config, because its TestMain never
// redirected HOME. Two consequences, and CI hit the second one: the pipeline's
// jobs share a self-hosted runner, two of them wrote that one config file at
// the same time, and both tests died with
//
//	Apply: step 2 (mark git-safe) failed: exit status 255
//
// on a tip whose local gate had run the same suite green — recorded as an
// escape. The first consequence is quieter and worse: every run of this suite
// appends safe.directory entries to a real person's ~/.gitconfig.
//
// internal/cli's TestMain already redirects HOME for exactly this reason.
func TestHome_IsRedirectedAwayFromTheOperatorsRealOne(t *testing.T) {
	real, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if realHomeAtStart == "" {
		t.Fatal("TestMain recorded no real home, so this test cannot tell whether it was redirected")
	}
	if filepath.Clean(real) == filepath.Clean(realHomeAtStart) {
		t.Errorf("HOME is still the operator's own %q — `git config --global` from this suite writes their real gitconfig, and two concurrent jobs writing it fail with exit 255", real)
	}
}

// ...and git agrees: the global config path it resolves must sit under the
// redirected home, since that is the file the prepare step actually writes.
func TestGitGlobalConfig_ResolvesUnderTheRedirectedHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "config", "--global", "--list", "--show-origin").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "file:") {
		return // no global config yet under the fresh home, which is also isolation
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		origin, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "file:"), "\t")
		if !ok || origin == "" {
			continue
		}
		if !strings.HasPrefix(filepath.Clean(origin), filepath.Clean(home)) {
			t.Errorf("git reads global config from %q, outside the redirected home %q", origin, home)
		}
	}
}
