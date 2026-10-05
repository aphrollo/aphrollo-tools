package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// guideEvents is the guide events of the repo's log as law|file pairs.
func guideEvents(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, e := range ReadEvents(root) {
		if e.Kind == "guide" {
			out = append(out, e.Detail["rule"]+"|"+e.Detail["file"]+"|"+e.Stage)
		}
	}
	return out
}

// An edit that leaves a commit refusal leaves one guide event naming the law
// and the file, which is what the measure of refusals the edit check missed
// matches a commit's refused pairs against.
func TestPostEdit_ARatchetRefusalNoteLeavesAGuideEventNamingTheLawAndFile(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, "package m\n\nfunc Size() int { return forbidden() + forbidden() }\n")

	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	got := guideEvents(t, root)
	if len(got) != 1 || got[0] != "ratchet:no-forbidden|widget.go|postedit" {
		t.Fatalf("guide events = %v, want one for no-forbidden at widget.go", got)
	}
}

func TestLogLawGuides_RecordsWhatNoDenyEventCarries(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawRepo(t)
	raw, _ := json.Marshal(map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": filepath.Join(root, "widget.go")}})
	found := []LawFinding{{Law: "denied", Deny: true, File: "widget.go"}, {Law: "warned", File: "widget.go"}, {Law: "denied", Deny: true, File: "widget.go"}}

	// The refused write: the deny event names "denied", so the guide is the rest.
	LogLawGuides(raw, Decision{Action: Block, Policy: "ratchet:denied"}, found)
	if got := guideEvents(t, root); len(got) != 1 || got[0] != "ratchet:warned|widget.go|preedit" {
		t.Fatalf("after a refusal: %v, want only the warned law", got)
	}

	// A write that went ahead: every distinct law once.
	LogLawGuides(raw, Decision{Action: Warn, Policy: "ratchet:warned"}, found)
	if got := guideEvents(t, root); len(got) != 3 {
		t.Fatalf("after a warning: %v, want the first guide plus denied and warned once each", got)
	}
}

func TestLogLawGuides_NothingFoundIsNoEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := lawRepo(t)
	LogLawGuides([]byte(`{}`), Decision{}, nil)
	if got := guideEvents(t, root); len(got) != 0 {
		t.Fatalf("guide events = %v, want none", got)
	}
}

func TestSlashPath_ABackslashIsASlash(t *testing.T) {
	if got := slashPath(`internal\tdd\a.go`); got != "internal/tdd/a.go" {
		t.Fatalf("slashPath = %q, want forward slashes", got)
	}
}

// A deny of a file below the repo root names it with forward slashes, whatever
// the host's separator: the commit refusal's event names it that way, and the
// measure matches the two.
func TestLogEditDecision_TheDenyEventNamesAFileBelowTheRootWithForwardSlashes(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := goRepo(t, "proj", "main")
	if err := os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"tool_input": map[string]string{"file_path": filepath.Join(repo, "sub", "dir", "a_test.go")}})

	LogEditDecision(raw, Decision{Action: Block, Policy: "ratchet:no-todo", Reason: "r", Escapes: []string{"smell-escape:x"}})

	var files []string
	for _, e := range ReadEvents(repo) {
		if f := e.Detail["file"]; f != "" {
			files = append(files, f)
		}
	}
	if len(files) != 2 || files[0] != "sub/dir/a_test.go" || files[1] != "sub/dir/a_test.go" {
		t.Fatalf("event files = %v, want the deny and the escape event both at sub/dir/a_test.go", files)
	}
}
