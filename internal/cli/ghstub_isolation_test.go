package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This package's issue and stats verbs shell out to gh, and only the tests
// that arrange a stub were isolated from the operator's real one. Under
// mutation the guards in front of those calls are exactly what gets inverted,
// which is how three real issues came to be filed against this repository by
// nobody (#155, #196, #197 — internal/tdd's own fixture strings, two of them
// four seconds apart while mutation jobs were starting). The stub goes in
// front of gh for the whole package, so reaching GitHub is impossible rather
// than merely unlikely.
func TestGh_ResolvesToTheStubForEveryTestInThePackage(t *testing.T) {
	stub, err := ghStubDir()
	if err != nil {
		t.Fatal(err)
	}
	found, err := exec.LookPath("gh")
	if err != nil {
		return // unresolvable is unreachable, which is the point
	}
	if !strings.HasPrefix(filepath.Clean(found), filepath.Clean(stub)) {
		t.Errorf("gh resolves to %q, outside the package stub at %q — a mutated guard reaches the real GitHub", found, stub)
	}
}
