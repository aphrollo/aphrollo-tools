//go:build windows

package msys

import (
	"os/exec"
	"testing"
	"time"
)

// The anchor is a shell that stays alive until it is let go, so that the first
// MSYS process of a test run is one whose temp directory outlives the run.
func TestStartAnchor_HoldsAShellUntilReleased(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on the PATH, so no MSYS /tmp to protect") // skip-ok: the anchor is for boxes that have the shell tools
	}
	release, exited := startAnchor()

	select {
	case <-exited:
		t.Fatal("the anchor shell ended on its own")
	case <-time.After(500 * time.Millisecond):
	}
	release()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the anchor shell outlived its release")
	}
}
