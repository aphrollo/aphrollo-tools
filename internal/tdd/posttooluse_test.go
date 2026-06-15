package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRun returns a SuiteRunner that ignores its inputs and yields a fixed
// result, so PostEdit can be exercised without spawning a real test suite.
func fakeRun(passed bool, output string) SuiteRunner {
	return func(Runner, string) SuiteResult { return SuiteResult{Passed: passed, Output: output} }
}

// postPayload builds a PostToolUse payload for a Go project at root.
func postPayload(tool, file string) []byte {
	in := map[string]any{
		"session_id": "sess-post",
		"tool_name":  tool,
		"tool_input": map[string]any{"file_path": file},
	}
	b, _ := json.Marshal(in)
	return b
}

func TestPostEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	test := filepath.Join(root, "widget_test.go")

	cases := []struct {
		name        string
		payload     []byte
		passed      bool
		output      string
		wantSilent  bool
		wantContain string
	}{
		{"green source is silent", postPayload("Edit", src), true, "ok\nPASS", true, ""},
		{"passing test edit is silent", postPayload("Write", test), true, "ok\nPASS", true, ""},
		{"failing source reports red", postPayload("Edit", src), false, "--- FAIL: TestThing\n want 1", false, "outcome=red"},
		{"missing impl is clean red", postPayload("Edit", src), false, "undefined: NewWidget", false, "red-missing-impl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PostEdit(c.payload, fakeRun(c.passed, c.output))
			if c.wantSilent {
				if got != "" {
					t.Fatalf("expected silence, got: %s", got)
				}
				return
			}
			if !strings.Contains(got, c.wantContain) {
				t.Fatalf("output missing %q:\n%s", c.wantContain, got)
			}
		})
	}
}

func TestPostEdit_SkipsNonActionable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	// A non-code file is ignored entirely.
	if got := PostEdit(postPayload("Edit", filepath.Join(root, "README.md")), fakeRun(false, "boom")); got != "" {
		t.Fatalf("ignore file should be silent, got: %s", got)
	}
	// A tool that itself failed has nothing to test.
	in := map[string]any{
		"session_id":    "s",
		"tool_name":     "Edit",
		"tool_input":    map[string]any{"file_path": filepath.Join(root, "x.go")},
		"tool_response": map[string]any{"success": false},
	}
	b, _ := json.Marshal(in)
	if got := PostEdit(b, fakeRun(false, "boom")); got != "" {
		t.Fatalf("failed tool response should be silent, got: %s", got)
	}
	// Malformed JSON fails silent.
	if got := PostEdit([]byte("{bad"), fakeRun(false, "boom")); got != "" {
		t.Fatalf("malformed input should be silent, got: %s", got)
	}
}

func TestPostEdit_StampsState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")

	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))
	s, _ := loadSession("sess-post")
	if got := s.ByProject[root].Outcome; got != string(Green) {
		t.Fatalf("state outcome = %q, want green", got)
	}
}

func TestRenderPostToolUse(t *testing.T) {
	if b, code := RenderPostToolUse(""); b != nil || code != 0 {
		t.Fatalf("empty text should be silent, got (%q,%d)", b, code)
	}
	b, code := RenderPostToolUse("hello")
	if code != 0 {
		t.Fatalf("post never blocks, exit = %d", code)
	}
	var out postToolUseOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput.AdditionalContext != "hello" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
}
