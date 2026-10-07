package mutation

import (
	"io"
	"strings"
	"testing"
	"time"
)

// The gate log carries how long a measurement took, so the report can show the
// time mutation costs; a verdict logged with 0 seconds hides it.
func TestFinishMeasure_LogsTheTimeTheMeasurementTook(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	now := time.Unix(1_700_000_000, 0)
	prev := measureNowFn
	measureNowFn = func() time.Time { return now }
	t.Cleanup(func() { measureNowFn = prev })

	finishMeasure(t.TempDir(), MutantsConfig{}, nil, io.Discard, now.Add(-90*time.Second))

	log := gateLogText(t, "")
	var line string
	for l := range strings.SplitSeq(log, "\n") {
		if strings.Contains(l, " mutants ") {
			line = l
		}
	}
	if !strings.HasSuffix(line, " 90.0s") {
		t.Errorf("the mutants line = %q, want it to end in 90.0s", line)
	}
}
