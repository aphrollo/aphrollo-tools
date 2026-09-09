package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// postEditRun drives the real post-edit gate over a throwaway project with a
// suite runner that returns a fixed result — the same seam PostEdit's own
// tests use. Nothing about `gate output` is faked here: the record it serves
// is the one the gate itself wrote while judging this edit.
func postEditRun(t *testing.T, root, output string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"session_id": "sess-output",
		"tool_name":  "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(root, "widget.go")},
	})
	if err != nil {
		t.Fatal(err)
	}
	tdd.PostEdit(payload, func(tdd.Runner, string) tdd.SuiteResult {
		return tdd.SuiteResult{Passed: false, Output: output}
	})
}

// goProject makes a directory the gate resolves as a project root.
func goProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// The whole point of the verb: the assertion text the gate's own run printed
// is available WITHOUT re-running the suite, which is what the narrowing rule
// refuses. `gate stats` answers the verdict; this answers the text.
func TestGateOutput_ServesTheRunTheGateJustMade(t *testing.T) {
	gateConfigDir(t)
	root := goProject(t)
	inDir(t, root)
	postEditRun(t, root, "--- FAIL: TestWidget\n    specimen.rs:355: assertion failed: left == right\n")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "output"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	// Header first (so a reader can date and attribute the bytes), then the
	// run's own text, unfiltered.
	for _, want := range []string{"stage:", "verdict:", "command:", "specimen.rs:355", "assertion failed: left == right"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q, got:\n%s", want, out.String())
		}
	}
}

// Nothing retained is not an empty success: it exits non-zero and says in
// ONE line which of the two reasons it is, so a session knows whether to
// wait for a gate run or to stop asking.
func TestGateOutput_WithNothingRetainedSaysWhyAndFails(t *testing.T) {
	gateConfigDir(t)
	inDir(t, goProject(t))

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "output"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("exit = 0 with nothing retained; want non-zero\nstdout: %s", out.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout must stay empty when there is nothing to print, got:\n%s", out.String())
	}
	msg := strings.TrimRight(errb.String(), "\n")
	if !strings.Contains(msg, "no gate run output recorded") {
		t.Errorf("stderr = %q, want it to name why there is nothing to serve", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("stderr must be one line, got:\n%s", msg)
	}
}

// A flag the verb does not have is a usage error, not a silent no-op —
// the same contract every other read-only gate verb keeps.
func TestGateOutput_UnknownFlagIsUsageError(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "output", "--bogus"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown flag\nstderr: %s", code, errb.String())
	}
}

// The surface table is what CLAUDE.md, the doctor report and the usage text
// are all checked against, so a verb missing from it is a verb nobody is
// told about.
func TestGateOutput_IsOnTheGateVerbSurface(t *testing.T) {
	if !slices.Contains(GateVerbs(), "output") {
		t.Fatalf("gate verb table = %v, want it to carry output", GateVerbs())
	}
	if !strings.Contains(gateUsage, "output") {
		t.Error("gate usage text must list the output subcommand")
	}
}
