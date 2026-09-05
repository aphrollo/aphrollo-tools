package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bashPayload(t *testing.T, session, cwd, command string) []byte {
	t.Helper()
	return bashPayloadID(t, session, "toolu_single", cwd, command)
}

func bashPayloadID(t *testing.T, session, toolUseID, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id":  session,
		"tool_use_id": toolUseID,
		"cwd":         cwd,
		"tool_name":   "Bash",
		"tool_input":  map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// powerShellPayload is bashPayload's PowerShell twin, same tool_input shape
// under a different tool_name: the PowerShell tool is classified exactly
// like Bash (issue #118).
func powerShellPayload(t *testing.T, session, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id": session,
		"cwd":        cwd,
		"tool_name":  "PowerShell",
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

// Claude batches tool calls, so two Bash calls interleave as PreA, PreB,
// PostA, PostB. One snapshot slot per SESSION means B's Pre overwrites A's and
// A's Post consumes it, leaving B's shell edit with no snapshot at all — the
// suite never runs for it. The snapshot belongs to the CALL, not the session.
func TestPostBashMatchesTheSnapshotToItsOwnToolCall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n\nfunc A() int { return 1 }\n")
	write(t, root, "b.go", "package m\n\nfunc B() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "two")

	PreBash(bashPayloadID(t, "s9", "toolu_A", root, "sed -i s/1/2/ a.go"))
	PreBash(bashPayloadID(t, "s9", "toolu_B", root, "sed -i s/1/2/ b.go"))
	write(t, root, "a.go", "package m\n\nfunc A() int { return 2 }\n")
	var seenA []Runner
	PostBash(bashPayloadID(t, "s9", "toolu_A", root, "sed -i s/1/2/ a.go"), runsAt(&seenA, root))

	write(t, root, "b.go", "package m\n\nfunc B() int { return 2 }\n")
	var seenB []Runner
	PostBash(bashPayloadID(t, "s9", "toolu_B", root, "sed -i s/1/2/ b.go"), runsAt(&seenB, root))
	if len(seenB) != 1 {
		t.Fatalf("the second batched call ran the suite %d times, want 1: %+v", len(seenB), seenB)
	}
}

// A snapshot whose Post never arrives (a cancelled call, a crashed hook) must
// not accumulate in the session file forever.
func TestPreBashKeepsABoundedNumberOfSnapshots(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	for i := range maxBashSnapshots + 5 {
		PreBash(bashPayloadID(t, "s10", fmt.Sprintf("toolu_%02d", i), root, "true"))
	}
	s, _ := loadSession("s10")
	if len(s.Bash) > maxBashSnapshots {
		t.Fatalf("held %d snapshots, want at most %d", len(s.Bash), maxBashSnapshots)
	}
	// The newest survives: an abandoned old one is what gets dropped.
	if _, ok := s.Bash[fmt.Sprintf("toolu_%02d", maxBashSnapshots+4)]; !ok {
		t.Fatalf("the newest snapshot was evicted: %v", s.Bash)
	}
}

// The snapshot must cost the same on a huge repo as on a small one: git is
// asked ONCE for what is dirty, and only those paths are stat'd. Asking for
// every tracked file made a `ls` cost two full index walks and a stat per
// source file in the tree.
func TestPreBashStatsOnlyWhatIsDirty(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "clean.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "clean")
	write(t, root, "dirty.go", "package m\n\nfunc D() {}\n")

	PreBash(bashPayloadID(t, "s11", "toolu_x", root, "true"))
	s, _ := loadSession("s11")
	snap := s.Bash["toolu_x"]
	if snap == nil {
		t.Fatal("a repo must be snapshotted")
	}
	if _, ok := snap.Dirty["dirty.go"]; !ok {
		t.Errorf("an untracked source file is dirty and must be stamped: %v", snap.Dirty)
	}
	if _, ok := snap.Dirty["clean.go"]; ok {
		t.Errorf("a committed, unmodified file is not stamped: %v", snap.Dirty)
	}
}

// The other half of the same cost fix: editing a file that was ALREADY dirty
// is the common case, and a bare porcelain hash cannot see it — the status
// line is identical before and after.
func TestPostBashNoticesASecondEditToAnAlreadyDirtyFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")

	PreBash(bashPayloadID(t, "s12", "toolu_y", root, "sed -i s/2/3/ widget.go"))
	touchLater(t, filepath.Join(root, "widget.go"), "package m\n\nfunc Widget() int { return 3 }\n")

	var seen []Runner
	PostBash(bashPayloadID(t, "s12", "toolu_y", root, "sed -i s/2/3/ widget.go"), runsAt(&seen, root))
	if len(seen) != 1 {
		t.Fatalf("the suite ran %d times, want 1: %+v", len(seen), seen)
	}
}

// touchLater rewrites a file and pushes its mtime forward, so a stamp
// comparison is not defeated by a coarse filesystem timestamp.
func touchLater(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
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

// TestPostBash_NamesEveryRootAChangeTouchedEvenWhenOneDefers pins issue #306:
// PostBash used to break after the FIRST root whose phase deferred, leaving
// every other root a single Bash command touched silently unexercised — with
// nothing beyond a per-file "bash-edit:" log line to say so. The loop still
// stops after one deferred phase (a second project's cold build here would
// run unwatched beside the first, and neither result would describe the tree
// by the time it lands), but it now names every root left untested.
func TestPostBash_NamesEveryRootAChangeTouchedEvenWhenOneDefers(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "pkga/go.mod", "module pkga\n\ngo 1.21\n")
	write(t, root, "pkga/a.go", "package pkga\n\nfunc A() int { return 1 }\n")
	write(t, root, "pkgb/go.mod", "module pkgb\n\ngo 1.21\n")
	write(t, root, "pkgb/b.go", "package pkgb\n\nfunc B() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "two roots")

	PreBash(bashPayload(t, "s-roots", root, "sed -i s/1/2/ pkga/a.go pkgb/b.go"))
	write(t, root, "pkga/a.go", "package pkga\n\nfunc A() int { return 2 }\n")
	write(t, root, "pkgb/b.go", "package pkgb\n\nfunc B() int { return 2 }\n")

	spawned := fakePhases(t) // no outcome queued: the spawned phase stays running

	text := PostBash(bashPayload(t, "s-roots", root, "sed -i s/1/2/ pkga/a.go pkgb/b.go"), fakeRun(true, "ok"))

	if len(*spawned) != 1 {
		t.Fatalf("spawned %d phases, want exactly 1 — a second project's cold build must not start unwatched beside the first", len(*spawned))
	}
	pkgbRoot := filepath.Join(root, "pkgb")
	if !strings.Contains(text, pkgbRoot) {
		t.Fatalf("advisory = %q, want it to name %s as a root this command changed but never ran a gate for", text, pkgbRoot)
	}
	if !strings.Contains(text, "skipped") {
		t.Fatalf("advisory = %q, want it to say the other root was skipped", text)
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
	if len(s.Bash) != 0 {
		t.Fatalf("no repo means nothing to snapshot, got %+v", s.Bash)
	}
}

// The PowerShell primary-checkout classification (item 7) needs the hook to
// fire for the PowerShell tool at all -- PrimaryCheckoutDecision already
// branches on tool_name internally, but nothing invokes it without a
// PreToolUse matcher for "PowerShell" in the installed settings.json.
func TestPatchSettings_WiresPowerShellForPreToolUse(t *testing.T) {
	out, changed, err := PatchSettings(nil, "/usr/local/bin/aphrollo")
	if err != nil || !changed {
		t.Fatalf("PatchSettings: changed = %v, err = %v", changed, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var hasPowerShell bool
	for _, g := range doc.Hooks["PreToolUse"] {
		if g.Matcher == "PowerShell" {
			hasPowerShell = true
		}
	}
	if !hasPowerShell {
		t.Errorf("PreToolUse matchers = %v, want a PowerShell entry so the primary-checkout classification runs for it", doc.Hooks["PreToolUse"])
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

	write(t, root, "extra.go", "package m\n\nfunc E() {}\n")

	PreBash(bashPayload(t, "s7", root, "true"))
	s, _ := loadSession("s7")
	snap := s.Bash["toolu_single"]
	if snap == nil {
		t.Fatal("a repo must be snapshotted")
	}
	if _, ok := snap.Dirty["extra.go"]; !ok {
		t.Errorf("a dirty source file is stamped: %v", snap.Dirty)
	}
	if _, ok := snap.Dirty["NOTES.md"]; ok {
		t.Errorf("a doc is not source: %v", snap.Dirty)
	}
	if _, ok := snap.Dirty["dist/generated.go"]; ok {
		t.Errorf("build output is never source: %v", snap.Dirty)
	}
}
