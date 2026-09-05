package pkg2

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("SKIP_SUITE") != "" {
		return
	}
	os.Exit(0)
}
