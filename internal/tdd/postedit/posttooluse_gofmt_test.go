package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	unformattedGo = "package widget\n\nfunc  Size( )int{return 1}\n"
	formattedGo   = "package widget\n\nfunc Size() int { return 1 }\n"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An agent's edit that leaves a .go file unformatted is formatted in place by
// the edit hook, and the edit's gate line says so: the commit gate's gofmt
// stage then has nothing left to refuse.
func TestPostEdit_GofmtFormatsAnUnformattedGoEditAndSaysSo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, unformattedGo)

	got := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	if body := readString(t, src); body != formattedGo {
		t.Fatalf("file not gofmt-formatted:\n%q\nwant\n%q", body, formattedGo)
	}
	if !strings.Contains(got, "→ green") || !strings.HasSuffix(got, " (gofmt formatted "+src+")") {
		t.Fatalf("the gate line must carry the verdict, then the gofmt note, got:\n%q", got)
	}
}

// A red summary runs over several lines; the note belongs on the first, the
// gate line, never tacked onto the end of the runner's output snippet.
func TestPostEdit_GofmtNoteSitsOnTheGateLineOfAMultiLineRed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, unformattedGo)

	got := PostEdit(postPayload("Edit", src), fakeRun(false, "--- FAIL: TestThing\n want 1"))

	line := firstLine(got)
	if !strings.Contains(line, "outcome=red") || !strings.HasSuffix(line, " (gofmt formatted "+src+")") {
		t.Fatalf("the red gate line must end in the gofmt note, got:\n%s", got)
	}
	if strings.Count(got, "gofmt formatted") != 1 {
		t.Fatalf("the note must appear once, got:\n%s", got)
	}
}

// With nothing to test (no project around the file) the edit still changed
// the file, so the note is the whole line rather than lost.
func TestPostEdit_GofmtNoteStandsAloneWhenNothingRan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	src := filepath.Join(t.TempDir(), "loose.go")
	mustWrite(t, src, unformattedGo)

	got := PostEdit(postPayload("Write", src), fakeRun(false, "must not run"))

	if want := "gate: gofmt formatted " + src; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if body := readString(t, src); body != formattedGo {
		t.Fatalf("file not formatted: %q", body)
	}
}

// Files the hook must leave byte-for-byte alone, and whose gate line must not
// claim a format that did not happen.
func TestPostEdit_GofmtLeavesFilesItMustNotTouch(t *testing.T) {
	cases := []struct {
		name, file, content string
	}{
		{"already formatted", "widget.go", formattedGo},
		{"does not parse: the compile error belongs to the test run", "widget.go", "package widget\n\nfunc  Size( {\n"},
		{"a formatted CRLF file is not rewritten to LF", "widget.go", strings.ReplaceAll(formattedGo, "\n", "\r\n")},
		{"not a .go file", "notes.txt", unformattedGo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root := mkProject(t, "go.mod")
			path := filepath.Join(root, c.file)
			mustWrite(t, path, c.content)

			got := PostEdit(postPayload("Edit", path), fakeRun(true, "ok\nPASS"))

			if body := readString(t, path); body != c.content {
				t.Fatalf("file changed:\n%q\nwas\n%q", body, c.content)
			}
			if strings.Contains(got, "gofmt formatted") {
				t.Fatalf("no format happened, yet the line says one did:\n%s", got)
			}
		})
	}
}

// A CRLF checkout keeps its line endings: the file is formatted, not converted.
func TestPostEdit_GofmtKeepsCRLFLineEndings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, strings.ReplaceAll(unformattedGo, "\n", "\r\n"))

	PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	if body, want := readString(t, src), strings.ReplaceAll(formattedGo, "\n", "\r\n"); body != want {
		t.Fatalf("got %q, want %q", body, want)
	}
}

// A tool call that failed changed nothing the agent meant, so nothing is
// formatted either.
func TestPostEdit_GofmtSkipsAFailedToolCall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, unformattedGo)
	payload, err := json.Marshal(map[string]any{
		"session_id":    "s",
		"tool_name":     "Edit",
		"tool_input":    map[string]any{"file_path": src},
		"tool_response": map[string]any{"success": false},
	})
	if err != nil {
		t.Fatal(err)
	}

	PostEdit(payload, fakeRun(true, "ok\nPASS"))

	if body := readString(t, src); body != unformattedGo {
		t.Fatalf("a failed edit was formatted: %q", body)
	}
}

// The note goes at the end of the first line even when that line is empty:
// the text's first newline ends it, wherever that newline sits.
func TestWithGofmtNote_EndsTheFirstLineEvenWhenItIsEmpty(t *testing.T) {
	if got, want := withGofmtNote("\nbody", "gofmt formatted x.go"), " (gofmt formatted x.go)\nbody"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
