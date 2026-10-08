package suite

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// A vitest run over thousands of tests prints a line per file and a summary
// last; a builder who piped the related run through `tail` saw it cut short.
// The gate itself must never be the one that cuts: what the child printed,
// stdout and stderr interleaved as it arrived, is what the result carries.
// Serial: swaps the package's process-wide suiteChildFn and waitForHeadroomFn.
func TestRunSuite_ALargeVitestRunComesBackWholeAndInOrder(t *testing.T) {
	prevWait, prevChild := waitForHeadroomFn, suiteChildFn
	t.Cleanup(func() { waitForHeadroomFn, suiteChildFn = prevWait, prevChild })
	waitForHeadroomFn = func(string, time.Duration) string { return "" }
	var want strings.Builder
	for i := range 40000 {
		fmt.Fprintf(&want, " ✓ src/lib/module%05d.test.ts (3 tests) %dms\n", i, i%90)
	}
	want.WriteString(" Test Files  40000 passed (40000)\n      Tests  120000 passed (120000)\n")
	suiteChildFn = func(spec run.Spec, _ MemCap) suiteChildEnd {
		_, _ = spec.Stdout.Write([]byte(want.String()[:want.Len()/2]))
		_, _ = spec.Stderr.Write([]byte(want.String()[want.Len()/2:]))
		return suiteChildEnd{}
	}

	res := RunSuite(time.Minute)(Runner{Cmd: "npx", Args: []string{"vitest", "related", "a.ts", "--run"}}, t.TempDir())

	if !res.Passed || res.Output != want.String() {
		t.Errorf("passed=%v, output is %d bytes, want the %d the run printed, in order", res.Passed, len(res.Output), want.Len())
	}
}
