package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// A measurement whose caller gave up ends the mutation tool and everything the
// tool started. The tool is a launcher of launchers (cargo-mutants starts
// cargo, which starts the test binaries), and a test binary left behind in
// kernel exit holds its memory for hours: the shell chain stands for that tree.
func TestRunMutantsTool_CancelEndsTheToolsWholeProcessTree(t *testing.T) {
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the tool under test is a shell, which a box may lack.
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "chain.sh")
	if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.ToSlash(filepath.Join(dir, "pids"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := runMutantsTool(ctx, dir, os.Environ(), []string{bash, filepath.ToSlash(script), pidFile, "wait"}, io.Discard)
		done <- result{code, err}
	}()

	runtest.WaitPids(pidFile, 3, 30*time.Second)
	cancel()
	select {
	case r := <-done:
		if r.err != nil || r.code == 0 {
			t.Fatalf("code = %d, err = %v, want the non-zero exit of a tool that was ended", r.code, r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not return within 30 s of its context ending")
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}
