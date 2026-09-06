package lonepkg

import (
	"os"
	"testing"
)

// No TestMain and no t.Setenv anywhere in this package — the trigger below
// has no isolation to be excused by.
func TestReadsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	_ = home
}
