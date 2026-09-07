package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateSelfCheck is `aphrollo gate selfcheck`: the install-time smoke test
// swapBinary runs against a candidate binary before it replaces the one
// installed (issue #532). It builds a marker-less temp tree — none of
// rootMarkers present, not even `.git` — and requires FindProjectRoot to
// come back empty for a file inside it. PR #528 pointed the go-runner
// scratch dir INSIDE a lane's own worktree, so `.git` became an ancestor of
// every fixture a `go test` child built there; every one of the ~35
// failures that caused reduces to this one assertion.
func runGateSelfCheck(args []string, stdout, stderr io.Writer) int { // args: unused, kept for dispatch symmetry
	dir, err := os.MkdirTemp("", "aphrollo-selfcheck-*")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate selfcheck: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "loose.go")
	if err := os.WriteFile(file, []byte("package m\n"), 0o600); err != nil {
		fmt.Fprintf(stderr, "aphrollo gate selfcheck: %v\n", err)
		return 2
	}

	if got := tdd.FindProjectRoot(file); got != "" {
		fmt.Fprintf(stdout, "aphrollo gate selfcheck: FindProjectRoot(%s) = %q, want \"\" for a marker-less tree (issue #532)\n", file, got)
		return 1
	}
	fmt.Fprintln(stdout, "aphrollo gate selfcheck: ok")
	return 0
}
