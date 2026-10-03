package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The recorded shell-call payload of the real harness carries a cwd and no
// file_path, so the compat guard judges the directory the session stands in.
func TestCompatHookDir_ReadsTheCwdOfARecordedBashPayload(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(recordedHookDir, "pretooluse_bash.json"))
	if err != nil {
		t.Fatal(err)
	}

	got := compatHookDir(raw)

	if want := `C:\Users\dev\spike-hooks`; got != want {
		t.Fatalf("compatHookDir = %q, want the recorded cwd %q", got, want)
	}
}
