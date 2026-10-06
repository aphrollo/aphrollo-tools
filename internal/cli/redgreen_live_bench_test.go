package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// TestPreToolUse_TimingReport times the whole PreToolUse hook (Run, in process) for a code
// edit of a lane, per mode, and logs its p50 and p95 in milliseconds. It is a
// measurement, not a check: it asserts nothing about time (a box under load would make it
// a flaky test), and runs only when RGL_TIMING is set. Every iteration edits a unit of its
// own, so the warn arm pays for its one guidance line each time. off is what the hook
// cost before the red→green rule was live.
func TestPreToolUse_TimingReport(t *testing.T) {
	if os.Getenv("RGL_TIMING") == "" {
		t.Skip("set RGL_TIMING=1 to print the PreToolUse timings") // skip-ok: a measurement, not a check
	}
	const n = 200
	for _, mode := range []struct{ name, arm, pin string }{
		{"off", tddarm.ArmWarn, "tdd = \"off\"\n"},
		{"warn", tddarm.ArmWarn, ""},
		{"enforce", tddarm.ArmEnforce, ""},
	} {
		dir := rglRepo(t, mode.arm, false)
		rglPin(t, mode.pin)
		payloads := make([]string, n)
		for i := range n {
			writeFile(t, filepath.Join(dir, fmt.Sprintf("pkg%d", i), "p.go"), "package pkg\n\nvar a = 1\n")
			payloads[i] = rglEdit(t, "rgl-time-"+mode.name, dir, fmt.Sprintf("pkg%d/p.go", i))
		}
		samples := make([]float64, 0, n)
		for _, p := range payloads {
			start := time.Now()
			var out, errb bytes.Buffer
			Run([]string{"gate", "pretooluse"}, strings.NewReader(p), &out, &errb)
			samples = append(samples, float64(time.Since(start))/float64(time.Millisecond))
		}
		sort.Float64s(samples)
		t.Logf("%-8s p50 %6.1f ms   p95 %6.1f ms   (%d edits, one unit each)", mode.name, samples[n/2], samples[n*95/100], n)
	}
}
