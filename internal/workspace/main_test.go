package workspace

import (
	"os"
	"testing"
)

// TestMain puts a REFUSING gh in front of the operator's real one for the
// whole package. This package shells out for `gh pr create`, `gh pr merge`,
// `gh pr edit` and `gh api`, and nothing but each test's own arrangement
// stood between a mutated guard and a real pull request being merged. The
// same exposure in internal/tdd filed three real issues against this repo
// from nobody (#155, #196, #197). See ghnet_test.go.
func TestMain(m *testing.M) {
	ghRefusalPath = installRefusingGh()
	code := m.Run()
	if ghRefusalPath != "" {
		os.RemoveAll(ghRefusalPath)
	}
	os.Exit(code)
}
