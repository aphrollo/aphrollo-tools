//go:build unix

package mutation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// TestWriteMeasureDiff_BatchesAPathListPastTheBudget is #960 for the diff
// cargo-mutants reads as --in-diff: a changed-file list whose command line
// would pass the budget runs as several git calls, each within it, and the
// file written is the patch one call over every path prints.
func TestWriteMeasureDiff_BatchesAPathListPastTheBudget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBinary(), append([]string{"-C", root}, args...)...)
		cmd.Env = cleanGitEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", args[0], err)
		}
		return string(out)
	}
	if err := os.MkdirAll(filepath.Join(root, "crates", "a", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	var files []string
	for i := range 150 {
		p := fmt.Sprintf("crates/a/src/pathspec_batching_fixture_module_%03d.rs", i)
		if err := os.WriteFile(filepath.Join(root, p), []byte("pub fn f() -> u32 { 0 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	run("add", "crates")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	base := strings.TrimSpace(run("rev-parse", "HEAD"))
	for i, p := range files {
		if err := os.WriteFile(filepath.Join(root, p), []byte(fmt.Sprintf("pub fn f() -> u32 { %d }\n", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := run(append([]string{"diff", base, "--"}, files...)...)

	var lengths []int
	defer setGitDiffOutForTest(func(dir string, args ...string) (string, string, error) {
		lengths = append(lengths, len(strings.Join(args, " ")))
		return gitDiffOut(dir, args...)
	})()

	path, err := writeMeasureDiff(root, base, files)
	if err != nil {
		t.Fatalf("writeMeasureDiff: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("batched patch differs from one call over every path (%d vs %d bytes)", len(got), len(want))
	}
	for i, n := range lengths {
		if n > argvbatch.Budget {
			t.Errorf("git call %d is %d chars, past the %d-char budget", i, n, argvbatch.Budget)
		}
	}
}
