package ghworkflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// A step that runs past its limit must leave nothing of what it started: on
// Windows an MSYS grandchild outlives `taskkill /T`, and a build the step had
// begun kept compiling after the run said it had stopped.
func TestRun_AStepThatHitsItsTimeoutLeavesNoProcessOfItsTreeRunning(t *testing.T) {
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the step is a shell chain, which a box may lack.
	}
	dir := t.TempDir()
	script := filepath.ToSlash(filepath.Join(dir, "chain.sh"))
	pidFile := filepath.ToSlash(filepath.Join(dir, "pids"))
	if err := os.WriteFile(filepath.Join(dir, "chain.sh"), []byte(runtest.BashChain), 0o755); err != nil {
		t.Fatal(err)
	}

	sum, out, _ := runFlow(t, "on: pull_request\njobs:\n  j:\n    steps:\n      - name: chain\n        shell: bash\n        run: bash '"+script+"' '"+pidFile+"' wait\n",
		func(o *Options) { o.StepTimeout = 8 * time.Second })

	pids := runtest.ReadPids(pidFile)
	t.Cleanup(func() {
		for _, pid := range pids {
			runtest.ForceKill(pid)
		}
	})
	if r := result(t, sum, "j"); r.Result != ResultFailure {
		t.Fatalf("j = %+v, want the step that ran past its limit to fail:\n%s", r, out)
	}
	if len(pids) < 3 {
		t.Fatalf("the chain recorded %d pids before the limit, want 3: it never came up\n%s", len(pids), out)
	}
	if !runtest.AllGone(pids, 20*time.Second) {
		t.Errorf("a pid of the step's shell chain %v outlived the step that was stopped at its limit", pids)
	}
}
