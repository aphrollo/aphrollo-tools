package siblingpkg

import (
	"os"
	"testing"
)

// Isolated by main_test.go, a SIBLING file — the case a file-at-a-time
// matcher cannot see.
func TestReadsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	_ = home
}
