package suite

import (
	"fmt"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func notrunCollapsePkgs(n int) []string {
	var pkgs []string
	for i := range n {
		pkgs = append(pkgs, fmt.Sprintf("./internal/pkg%02d", i))
	}
	return pkgs
}

// The NOT RUN line of a wide commit named every package twice, once in the
// command and once in the list: a session read 12 package names it had no use
// for. It now says how many and names one; the whole list is on the gate
// log's event, where `aphrollo why` prints it.
func TestReportSuitesNotRun_AWideListIsACountAndOneNameAndTheListStaysOnTheEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	pkgs := notrunCollapsePkgs(12)
	runner := Runner{Cmd: "go", Args: append([]string{"test", "-count=1"}, pkgs...)}

	stderr := tddtest.CaptureStderr(t, func() { reportSuitesNotRun("precommit", root, "package", runner, pkgs) })

	for _, want := range []string{"NOT RUN — 12 packages not tested here", "e.g. ./internal/pkg00", "the merge gate"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the line lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "pkg05") {
		t.Errorf("the line still names the packages one by one:\n%s", stderr)
	}
	var kept string
	for _, e := range ReadEvents(root) {
		if e.Verdict == "suites-not-run" {
			kept = e.Detail["not_run"]
		}
	}
	if want := strings.Join(pkgs, " "); kept != want {
		t.Errorf("the event keeps %q, want the whole list %q", kept, want)
	}
}

// A short list is as cheap as a count: it keeps being named.
func TestReportSuitesNotRun_AShortListIsStillNamedInFull(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	pkgs := notrunCollapsePkgs(3)
	runner := Runner{Cmd: "go", Args: append([]string{"test"}, pkgs...)}

	stderr := tddtest.CaptureStderr(t, func() { reportSuitesNotRun("precommit", root, "package", runner, pkgs) })

	if want := "NOT RUN — ./internal/pkg00, ./internal/pkg01, ./internal/pkg02 not tested here"; !strings.Contains(stderr, want) {
		t.Errorf("the line lacks %q:\n%s", want, stderr)
	}
}
