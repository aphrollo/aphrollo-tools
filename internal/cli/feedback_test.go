package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The verb has to be reachable, or the whole route is documentation. gh is
// unreachable in this package's tests, so a real filing cannot be exercised
// here — what must hold is that `gate feedback` is dispatched at all and
// refuses cleanly rather than being swallowed as an unknown command.
func TestGateFeedback_IsDispatchedAndRefusesAnEmptyTitle(t *testing.T) {
	var out, errb bytes.Buffer

	if code := runGate([]string{"feedback"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2 for a report with no title", code)
	}
	if !strings.Contains(errb.String(), "a report needs a title") {
		t.Errorf("stderr = %q, want it to say a title is required", errb.String())
	}
}

// The help has to say what distinguishes this from `gate issue`, because a
// session that cannot tell them apart files tool bugs in the wrong tracker —
// which is the whole failure this verb exists to end.
func TestGateFeedback_HelpSaysItFilesAgainstTheToolNotTheLocalRepo(t *testing.T) {
	var out, errb bytes.Buffer

	if code := runGate([]string{"feedback", "--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	help := out.String()
	for _, want := range []string{"TOOL", "upstream", "tip"} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not mention %q — a session must be able to tell this from `gate issue`", want)
		}
	}
}

// A session reaching for this verb types `aphrollo feedback`, the way it types
// `aphrollo issue`; that got `unknown command "feedback"` and a usage that
// never mentioned the verb, so reports went in by hand with gh (#717). The
// top level dispatches it and its usage names it.
func TestFeedback_IsATopLevelCommand(t *testing.T) {
	var out, errb bytes.Buffer

	if code := Run([]string{"feedback"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2 for a report with no title", code)
	}
	if !strings.Contains(errb.String(), "a report needs a title") {
		t.Errorf("stderr = %q, want the feedback verb's own refusal, not an unknown command", errb.String())
	}
	if !strings.Contains(rootUsage, "\n  feedback ") {
		t.Errorf("the top-level usage does not list feedback:\n%s", rootUsage)
	}
}
