package install

import (
	"encoding/json"
	"strings"
	"testing"
)

// A suite the agent runs by hand is counted from the shell call's own result: the
// harness sends it to PostToolUse when the call exited 0 and to PostToolUseFailure
// when it did not, so both events have to be wired, for the Bash and PowerShell tools.
func TestPatchSettings_WiresTheResultOfAHandRunSuiteForBashAndPowerShell(t *testing.T) {
	out, changed, err := PatchSettings(nil, "/usr/local/bin/aphrollo")
	if err != nil || !changed {
		t.Fatalf("PatchSettings: changed = %v, err = %v", changed, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	has := func(event, matcher, verb string) bool {
		for _, g := range doc.Hooks[event] {
			for _, h := range g.Hooks {
				if g.Matcher == matcher && strings.HasSuffix(h.Command, " "+verb) {
					return true
				}
			}
		}
		return false
	}
	for _, c := range []struct{ event, matcher, verb string }{
		{"PostToolUse", "Bash", "posttooluse"},
		{"PostToolUse", "PowerShell", "posttooluse"},
		{"PostToolUseFailure", "Bash|PowerShell", "posttoolusefailure"},
	} {
		if !has(c.event, c.matcher, c.verb) {
			t.Errorf("%s has no %q group running %s: %+v", c.event, c.matcher, c.verb, doc.Hooks[c.event])
		}
	}
	stripped, changed, err := StripSettings(out)
	if err != nil || !changed {
		t.Fatalf("StripSettings: changed = %v, err = %v", changed, err)
	}
	if strings.Contains(string(stripped), "posttoolusefailure") {
		t.Errorf("the PostToolUseFailure hook survived a strip:\n%s", stripped)
	}
}
