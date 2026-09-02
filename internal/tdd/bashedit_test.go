package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bashPayload(t *testing.T, session, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id": session,
		"cwd":        cwd,
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// An edit made by `sed` or a heredoc is still an edit: the hooks that judge a
// Write must judge it too, or the whole gate is one shell command away from
// being off. PreToolUse records what the tree looked like; PostToolUse diffs
// and puts every changed source file through the same path an Edit takes.
func TestPostBashRunsTheSuiteForAFileTheCommandChanged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	PreBash(bashPayload(t, "s1", root, "sed -i s/1/2/ widget.go"))
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")

	var seen []Runner
	text := PostBash(bashPayload(t, "s1", root, "sed -i s/1/2/ widget.go"), runsAt(&seen, root))
	if len(seen) != 1 {
		t.Fatalf("the suite ran %d times, want 1: %+v", len(seen), seen)
	}
	if !strings.Contains(text, "gate:") {
		t.Fatalf("a Bash edit must report like any other edit, got %q", text)
	}
	requireLoggedVerdict(t, cfg, "bash-edit:widget.go")
}

// A shell command that touches only docs or build output has nothing for the
// suite to say, and running one would make every `ls`-adjacent command cost a
// test build.
func TestPostBashIgnoresAChangeToANonSourceFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)

	PreBash(bashPayload(t, "s2", root, "echo hi >> NOTES.md"))
	write(t, root, "NOTES.md", "# notes\n")

	var seen []Runner
	if text := PostBash(bashPayload(t, "s2", root, "echo hi >> NOTES.md"), runsAt(&seen, root)); text != "" {
		t.Fatalf("a doc-only change reports nothing, got %q", text)
	}
	if len(seen) != 0 {
		t.Fatalf("no suite may run for a doc-only change: %+v", seen)
	}
}

// One command can rewrite twenty files. The project's suite answers for all
// of them at once, so it runs ONCE — a per-file loop would charge the same
// build twenty times, and (once deferral is on) leave twenty detached ones.
func TestPostBashRunsOneProjectsSuiteOnceForAMultiFileCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n\nfunc A() int { return 1 }\n")
	write(t, root, "b.go", "package m\n\nfunc B() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "two")

	PreBash(bashPayload(t, "s3", root, "gofmt -w ."))
	write(t, root, "a.go", "package m\n\nfunc A() int { return 2 }\n")
	write(t, root, "b.go", "package m\n\nfunc B() int { return 2 }\n")

	var seen []Runner
	PostBash(bashPayload(t, "s3", root, "gofmt -w ."), runsAt(&seen, root))
	if len(seen) != 1 {
		t.Fatalf("the suite ran %d times, want 1: %+v", len(seen), seen)
	}
}

// A brand-new source file is a change too — the snapshot has never seen it.
func TestPostBashNoticesAFileTheCommandCreated(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)

	PreBash(bashPayload(t, "s4", root, "cat > new.go"))
	write(t, root, "new.go", "package m\n\nfunc New() int { return 1 }\n")

	var seen []Runner
	PostBash(bashPayload(t, "s4", root, "cat > new.go"), runsAt(&seen, root))
	if len(seen) != 1 {
		t.Fatalf("the suite ran %d times, want 1: %+v", len(seen), seen)
	}
}

// No snapshot means no diff, and guessing would run a suite for a command
// that changed nothing. A Bash call outside any repo is skipped entirely.
func TestPostBashIsSilentWithoutASnapshot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	var seen []Runner
	if text := PostBash(bashPayload(t, "s5", root, "ls"), runsAt(&seen, root)); text != "" {
		t.Fatalf("without a PreToolUse snapshot the hook says nothing, got %q", text)
	}
	if len(seen) != 0 {
		t.Fatalf("no suite may run without a snapshot: %+v", seen)
	}
}

func TestPreBashSkipsADirectoryThatIsNotARepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	PreBash(bashPayload(t, "s6", dir, "echo hi"))

	s, _ := loadSession("s6")
	if s.Bash != nil {
		t.Fatalf("no repo means nothing to snapshot, got %+v", s.Bash)
	}
}

// The hooks have to be WIRED for any of this to fire, and settings.json holds
// one group per matcher: adding the Bash group must not evict the edit group
// that was already there.
func TestPatchSettingsWiresBashAlongsideTheEditMatchers(t *testing.T) {
	out, changed, err := PatchSettings(nil, "/usr/local/bin/aphrollo")
	if err != nil || !changed {
		t.Fatalf("PatchSettings: changed = %v, err = %v", changed, err)
	}
	matchers := map[string][]string{}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			matchers[event] = append(matchers[event], g.Matcher)
		}
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		var hasBash, hasEdit bool
		for _, m := range matchers[event] {
			hasBash = hasBash || m == "Bash"
			hasEdit = hasEdit || strings.Contains(m, "Edit")
		}
		if !hasBash || !hasEdit {
			t.Errorf("%s matchers = %v, want both the edit tools and Bash", event, matchers[event])
		}
	}

	again, changed, err := PatchSettings(out, "/usr/local/bin/aphrollo")
	if err != nil {
		t.Fatal(err)
	}
	if changed || string(again) != string(out) {
		t.Error("a second patch over its own output must be a no-op")
	}
}

// A tracked source file is what the snapshot covers; build output and
// gitignored trees would make every command look like an edit.
func TestPreBashSnapshotsTrackedSourceOnly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n")
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "files")
	if err := os.MkdirAll(filepath.Join(root, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "out/generated.go", "package out\n")

	PreBash(bashPayload(t, "s7", root, "true"))
	s, _ := loadSession("s7")
	if s.Bash == nil {
		t.Fatal("a repo must be snapshotted")
	}
	if _, ok := s.Bash.Files["widget.go"]; !ok {
		t.Errorf("tracked source is in the snapshot: %v", s.Bash.Files)
	}
	if _, ok := s.Bash.Files["NOTES.md"]; ok {
		t.Errorf("a doc is not source: %v", s.Bash.Files)
	}
	if _, ok := s.Bash.Files["out/generated.go"]; ok {
		t.Errorf("an untracked file is not in the snapshot: %v", s.Bash.Files)
	}
}
