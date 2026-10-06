package postedit

import (
	"strings"
	"testing"
)

// redCapBytes is the 400-token cap of a red gate line in bytes, by the repo's
// own brief counter (measure.Tokens: bytes plus three, over four).
const redCapBytes = 400*4 - 3

// A red line is paid for by every session on every red edit. It names the
// first failure and points at `aphrollo gate output`, where the run's whole
// text stays readable, and it never grows past 400 tokens: a long run used to
// put 2000 bytes of its output on the line (about 590 tokens).
func TestRedSummary_StaysUnderTheTokenCapAndPointsAtGateOutput(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := Runner{Cmd: "go", Args: []string{"test", "./internal/widget"}}
	cases := []struct {
		name    string
		outcome Outcome
		output  string
		want    string
	}{
		{"a long test failure", Red, buildLongNextestFailureOutput(t), "first failure: wall::t_x"},
		{"a long compile error", RedBogus, buildLongCompileErrorOutput(t), "error[E0425]: cannot find function `widget`"},
		{"a go test failure", Red, redcapGoFailure(), "first failure: TestWidget_Total"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redSummary(r, "D:/Projects/.worktrees/aphrollo-tools/lane", c.outcome, c.output)
			if len(got) > redCapBytes {
				t.Errorf("the red line is %d bytes, over the %d the 400-token cap allows:\n%s", len(got), redCapBytes, got)
			}
			for _, want := range []string{c.want, "aphrollo gate output"} {
				if !strings.Contains(got, want) {
					t.Errorf("the red line lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func redcapGoFailure() string {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("=== RUN   TestCase\n--- PASS: TestCase (0.00s)\n")
	}
	b.WriteString("=== RUN   TestWidget_Total\n    widget_test.go:42: Total() = 7, want 8\n--- FAIL: TestWidget_Total (0.00s)\nFAIL\nFAIL\tgithub.com/x/y/widget\t0.3s\n")
	return b.String()
}
