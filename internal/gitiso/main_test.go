package gitiso

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(Main(func() int { return m.Run() }))
}
