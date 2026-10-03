//go:build windows

package core

import (
	"path/filepath"
	"testing"
)

func TestStateRoot_WindowsUsesLocalAppDataBeforeTheXdgDirectory(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	t.Setenv("TRELLIS_DATA", "")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("XDG_STATE_HOME", filepath.Join("x", "state"))

	if got, want := StateRoot(), filepath.Join(local, "trellis"); got != want {
		t.Fatalf("StateRoot = %q, want %q", got, want)
	}
}
