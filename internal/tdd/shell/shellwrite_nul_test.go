package shell

import (
	"path/filepath"
	"testing"
)

// NUL is a reserved DOS device name in EVERY directory on Windows, not only at
// a bare `nul`: `> /nul`, `> C:\nul` and `> sub/nul` all write to the null
// device and none of them writes a file. resolveAgainst matched only the bare
// spelling, so a rooted one resolved to an ordinary absolute path and the
// guardrail read it as a write into the repo — the false block this file's
// contract rules out. Found by FuzzBashWriteTargets (issue #218).
func TestBashWriteTargets_ClaimsNothingForARootedNulDevice(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{">/nul", "echo hi > /nul", "echo hi > sub/nul", "echo hi > NUL"} {
		if got := bashWriteTargets(cmd, filepath.FromSlash("/repo/lane")); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want nothing — nul is the null device in any directory, so nothing is written", cmd, got)
		}
	}
}

// A dropped leading slash (`>dev/null`) is judged the same as the rooted
// spelling: the operand resolved onto cwd (`/repo/lane/dev/null`) and the
// guardrail claimed it as a real write into the repo — the false block this
// file's contract rules out. Found by FuzzBashWriteTargets (issue #413).
func TestBashWriteTargets_ClaimsNothingForAnUnrootedDevNull(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{">dev/null", "echo hi > dev/null"} {
		if got := bashWriteTargets(cmd, filepath.FromSlash("/repo/lane")); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want nothing — a dropped leading slash still names the null device, not a real write", cmd, got)
		}
	}
}

// A doubled separator (`dev//null`, `dev///null`) still names the null
// device once the repeated slashes collapse — the string comparison in
// isNullDevice missed this because it compared against the literal
// "dev/null" without normalising runs of separators first. Found by
// FuzzBashWriteTargets (issue #537).
func TestBashWriteTargets_ClaimsNothingForADoubledSeparatorDevNull(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{">dev//null", ">dev///null"} {
		if got := bashWriteTargets(cmd, filepath.FromSlash("/repo/lane")); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want nothing — a doubled separator still names the null device, not a real write", cmd, got)
		}
	}
}

// ...and the guard is on the DEVICE name, not on any path containing it: a
// file whose name merely starts with those letters is an ordinary write.
func TestBashWriteTargets_StillClaimsAFileNamedLikeTheDevice(t *testing.T) {
	t.Parallel()
	repo := filepath.FromSlash("/repo/lane")
	want := filepath.Join(repo, "nullable.txt")

	found := false
	for _, got := range bashWriteTargets("echo hi > nullable.txt", repo) {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bashWriteTargets did not claim %q — only the exact device name is a sink", want)
	}
}
