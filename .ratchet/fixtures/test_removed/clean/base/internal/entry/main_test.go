package entry

import (
	"os"
	"testing"
)

// TestMain is the entry point, not a test: removing it at tip retires no
// test and needs no tombstone.
func TestMain(m *testing.M) { os.Exit(m.Run()) }
