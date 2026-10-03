//go:build !windows

package core

import (
	"path/filepath"
	"testing"
)

// LOCALAPPDATA is a Windows variable: set on another system it names nothing
// the state root should follow.
func TestStateRoot_OffWindowsIgnoresLocalAppData(t *testing.T) {
	t.Setenv("TRELLIS_DATA", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
	t.Setenv("XDG_STATE_HOME", filepath.Join("x", "state"))

	if got, want := StateRoot(), filepath.Join("x", "state", "trellis"); got != want {
		t.Fatalf("StateRoot = %q, want %q", got, want)
	}
}
