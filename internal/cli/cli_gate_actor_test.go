package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// actorGoRepo is a git repo holding a one-file Go module, for an edit hook to
// answer in.
func actorGoRepo(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	file = filepath.Join(root, "a.go")
	for name, body := range map[string]string{"go.mod": "module actorfix\n\ngo 1.22\n", "a.go": "package actorfix\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, file
}

func actorsOfEdits(root string) []string {
	var actors []string
	for _, e := range tdd.ReadEvents(root) {
		if e.Kind == "edit" {
			actors = append(actors, e.Actor)
		}
	}
	return actors
}

// The report could not tell which builder worked in which lane: only
// hook.timing carried the agent. A PostToolUse edit event from a subagent's
// call records "session/agent"; the main session's records the session alone.
func TestPostToolUse_AnEditEventCarriesTheAgentOfTheCallThatMadeIt(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	root, file := actorGoRepo(t)
	call := func(agent string) {
		fields := map[string]any{"session_id": "sid-edit", "cwd": root, "tool_name": "Edit", "tool_input": map[string]any{"file_path": file}}
		if agent != "" {
			fields["agent_id"] = agent
		}
		offHookRun(t, "posttooluse", offPayload(t, fields))
	}

	call("aid-7")
	call("")

	got := actorsOfEdits(root)
	if len(got) != 2 || got[0] != "sid-edit/aid-7" || got[1] != "sid-edit" {
		t.Errorf("edit actors = %v, want [sid-edit/aid-7 sid-edit]", got)
	}
}
