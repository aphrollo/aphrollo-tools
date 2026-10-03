package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// goRepo is a Go project on the given branch, under its own state root, and the
// gate state dir the test logs into.
func goRepo(t *testing.T, name, branch string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		filepath.Join(".git", "HEAD"): "ref: refs/heads/" + branch + "\n",
		"go.mod":                      "module x\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// A project path with a space in it must reach the event log as the real path:
// the gate.log line needs the space-free token, the event does not, and a
// token names a directory that does not exist, so repo and lane were lost.
func TestLogOverride_EventKeepsTheRealPathOfASpacedProject(t *testing.T) {
	repo := goRepo(t, "my proj", "lane/spaced")

	LogOverride("override-off", "sess", repo)

	events := ReadEvents(repo)
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	if e := events[0]; filepath.ToSlash(e.Repo) != filepath.ToSlash(repo) || e.Lane != "lane/spaced" || e.Kind != "override" {
		t.Fatalf("repo/lane/kind = %q / %q / %q, want %q / lane/spaced / override", e.Repo, e.Lane, e.Kind, repo)
	}
}

// A refused edit leaves one deny event naming the rule, the family it belongs
// to and the override the refusal offered: "which rules fire, and could the
// agent have waived them" is a count of that event.
func TestLogEditDecision_ADenyEventNamesRuleCauseAndOfferedOverride(t *testing.T) {
	repo := goRepo(t, "proj", "main")
	file := filepath.Join(repo, "a_test.go")
	raw, err := json.Marshal(map[string]any{"tool_input": map[string]string{"file_path": file}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		policy, override string
		want             map[string]string
	}{
		{"test-sleep", "real-time:", map[string]string{"rule": "test-sleep", "cause": "smell", "override": "real-time:"}},
		{"test-skip", "", map[string]string{"rule": "test-skip", "cause": "smell", "override": "none"}},
		{"ratchet:no-todo", "law-escape-comment", map[string]string{"rule": "ratchet:no-todo", "cause": "law", "override": "law-escape-comment"}},
		{"primary-checkout", "", map[string]string{"rule": "primary-checkout", "cause": "wall", "override": "none"}},
	}

	for _, c := range cases {
		LogEditDecision(raw, Decision{Action: Block, Policy: c.policy, Override: c.override, Reason: "r"})
	}

	events := ReadEvents(repo)
	if len(events) != len(cases) {
		t.Fatalf("%d events, want %d", len(events), len(cases))
	}
	for i, c := range cases {
		if events[i].Kind != "deny" || !reflect.DeepEqual(events[i].Detail, c.want) {
			t.Errorf("%s: event = %+v, want a deny with detail %v", c.policy, events[i], c.want)
		}
	}
}

func TestLogBashSuiteDecision_ADenyEventNamesTheWallAndItsSwitch(t *testing.T) {
	repo := goRepo(t, "proj", "main")
	raw, err := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": repo, "tool_input": map[string]string{"command": "go test ./..."}})
	if err != nil {
		t.Fatal(err)
	}

	LogBashSuiteDecision(raw, Decision{Action: Block, Policy: "bash-whole-suite", Override: escapeBashEnvSwitch, Reason: "r"})

	events := ReadEvents(repo)
	want := map[string]string{"rule": "bash-whole-suite", "cause": "wall", "override": escapeBashEnvSwitch}
	if len(events) != 1 || events[0].Kind != "deny" || !reflect.DeepEqual(events[0].Detail, want) {
		t.Fatalf("events = %+v, want one deny with detail %v", events, want)
	}
}
