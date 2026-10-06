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

// An install made before the PostToolUseFailure hook existed gets it on the next
// install, once; a second install changes nothing.
func TestPatchSettings_AnInstallWithoutThePostToolUseFailureHookGetsItOnceAndIsStableAfter(t *testing.T) {
	const bin = "/usr/local/bin/aphrollo"
	full, _, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(full, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["hooks"].(map[string]any), "PostToolUseFailure")
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	once, changed, err := PatchSettings(old, bin)
	if err != nil || !changed {
		t.Fatalf("patching the old install: changed = %v, err = %v", changed, err)
	}
	if n := strings.Count(string(once), "posttoolusefailure"); n != 1 {
		t.Fatalf("the patched install runs posttoolusefailure %d times, want once:\n%s", n, once)
	}
	twice, changed, err := PatchSettings(once, bin)
	if err != nil || changed || string(twice) != string(once) {
		t.Errorf("a second patch: changed = %v, err = %v, bytes equal = %v, want unchanged", changed, err, string(twice) == string(once))
	}
}
