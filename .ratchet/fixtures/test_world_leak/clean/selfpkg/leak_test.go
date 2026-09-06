package selfpkg

import (
	"os"
	"testing"
)

// Isolated in its OWN file — the shape marker-within-lines could already
// have caught, kept here so marker-in-package proves it still does.
func TestReadsHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	_ = home
}
