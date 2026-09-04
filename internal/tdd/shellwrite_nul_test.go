package tdd

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
	for _, cmd := range []string{">/nul", "echo hi > /nul", "echo hi > sub/nul", "echo hi > NUL"} {
		if got := bashWriteTargets(cmd, filepath.FromSlash("/repo/lane")); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want nothing — nul is the null device in any directory, so nothing is written", cmd, got)
		}
	}
}

// ...and the guard is on the DEVICE name, not on any path containing it: a
// file whose name merely starts with those letters is an ordinary write.
func TestBashWriteTargets_StillClaimsAFileNamedLikeTheDevice(t *testing.T) {
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
