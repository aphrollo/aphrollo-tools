package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitMsgHook_BlocksWithANonZeroExit pins the hook contract: git honours
// the EXIT CODE, so a rejection that prints and exits 0 lets the commit land
// with the message it just objected to.
func TestCommitMsgHook_BlocksWithANonZeroExit(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"),
		[]byte("[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(repo, "COMMIT_EDITMSG")
	if err := os.WriteFile(msg, []byte("Fix the flaky retry timer\n\nCo-Authored-By: Someone <s@example.com>\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code := runGate([]string{"commitmsg", msg, "--repo", repo}, strings.NewReader(""), &out, &errBuf)
	if code == 0 {
		t.Fatalf("exit = 0, want a rejection; stderr: %s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Co-Authored-By") {
		t.Fatalf("stderr = %q, want the offending line quoted", errBuf.String())
	}

	if err := os.WriteFile(msg, []byte("Fix the flaky retry timer properly\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runGate([]string{"commitmsg", msg, "--repo", repo}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d for an ordinary message; stderr: %s", code, errBuf.String())
	}
}

// TestCommitMsgHook_NoArgumentPassesThrough pins the failure direction: git
// always passes the file, so a missing argument means something is wrong with
// the INSTALL, and wedging every commit over that is worse than the tell it
// was guarding.
func TestCommitMsgHook_NoArgumentPassesThrough(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := runGate([]string{"commitmsg"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, want a pass-through when the hook was called wrong", code)
	}
}
