package install

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// TestManagedEvents_PostToolUseHarnessTimeout_ExceedsGoDeadline pins a real
// incident (found in review 2026-08-15): the Claude Code harness kills a
// PostToolUse hook PROCESS from OUTSIDE once settings.json's own "timeout"
// elapses — entirely independent of RunSuite's Go-side context deadline. The
// template had drifted to a hardcoded 90s while DefaultPostEditTimeout had
// already moved to 100s, so the harness was killing the hook BEFORE Go's own
// deadline (and its WaitDelay cleanup) ever fired: no TIMEOUT line returned,
// no state stamped, and the spawned cargo process left orphaned. The break
// this test catches: "harness kills the hook before its own deadline" — the
// template timeout must exceed DefaultPostEditTimeout by a REAL margin
// (>= 10s), not just be numerically larger by one second.
func TestManagedEvents_PostToolUseHarnessTimeout_ExceedsGoDeadline(t *testing.T) {
	t.Parallel()
	for _, me := range managedEvents {
		if me.event != "PostToolUse" {
			continue
		}
		want := int(DefaultPostEditTimeout/time.Second) + 10
		if me.timeout < want {
			t.Fatalf("PostToolUse harness timeout = %ds, want >= %ds (DefaultPostEditTimeout=%s + 10s margin) — "+
				"the harness would kill the hook before RunSuite's own deadline ever fires",
				me.timeout, want, DefaultPostEditTimeout)
		}
		return
	}
	t.Fatal("no PostToolUse entry found in managedEvents")
}

// helper: parse settings JSON and return the hooks map for an event.
func hookGroups(t *testing.T, data []byte, event string) []any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	return groups
}

// commandStrings returns every hook command string under an event.
func commandStrings(t *testing.T, data []byte, event string) []string {
	t.Helper()
	var out []string
	for _, g := range hookGroups(t, data, event) {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, ok := hm["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func hasCommandContaining(t *testing.T, data []byte, event, sub string) bool {
	for _, c := range commandStrings(t, data, event) {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

const bin = "/usr/local/bin/aphrollo"

// Installing into an empty settings file wires all session-hook events to the
// aphrollo tdd subcommands.
func TestPatchSettings_InstallsAllEvents(t *testing.T) {
	t.Parallel()
	out, changed, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true installing into empty settings")
	}
	for _, want := range []struct{ event, sub string }{
		{"SessionStart", "$x\" gate sessionstart"},
		{"PreToolUse", "$x\" gate pretooluse"},
		{"PostToolUse", "$x\" gate posttooluse"},
		{"SessionEnd", "$x\" gate sessionend"},
		{"UserPromptSubmit", "$x\" gate userpromptsubmit"},
	} {
		if !hasCommandContaining(t, out, want.event, want.sub) {
			t.Errorf("%s: missing command %q\n%s", want.event, want.sub, out)
		}
	}
}

// A second patch over our own output is a no-op: changed=false, byte-identical.
func TestPatchSettings_Idempotent(t *testing.T) {
	t.Parallel()
	first, _, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	second, changed, err := PatchSettings(first, bin)
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false on re-patch, got true\n%s", second)
	}
	if string(first) != string(second) {
		t.Errorf("re-patch changed bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Foreign hooks (e.g. caveman on UserPromptSubmit) survive the patch; the
// aphrollo entry is added alongside, not in place of them.
func TestPatchSettings_PreservesForeignHooks(t *testing.T) {
	t.Parallel()
	in := []byte(`{
	  "hooks": {
	    "UserPromptSubmit": [
	      {"hooks": [{"type": "command", "command": "node caveman-mode-tracker.js"}]}
	    ]
	  },
	  "theme": "dark"
	}`)
	out, changed, err := PatchSettings(in, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "caveman-mode-tracker.js") {
		t.Errorf("dropped foreign caveman hook\n%s", out)
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "$x\" gate userpromptsubmit") {
		t.Errorf("missing aphrollo userpromptsubmit\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || m["theme"] != "dark" {
		t.Errorf("dropped unrelated top-level key 'theme'\n%s", out)
	}
}

// Old claude-code-tdd Node hook entries are migrated out: after the patch the
// only TDD commands are the aphrollo ones, the node tdd-*.js entries are gone.
func TestPatchSettings_MigratesNodeHooks(t *testing.T) {
	t.Parallel()
	in := []byte(`{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Edit|Write", "hooks": [
	        {"type": "command", "command": "/opt/node/bin/node /home/x/.claude/hooks/tdd-pre-edit.js"}
	      ]}
	    ]
	  }
	}`)
	out, _, err := PatchSettings(in, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if hasCommandContaining(t, out, "PreToolUse", "tdd-pre-edit.js") {
		t.Errorf("old node tdd hook not migrated out\n%s", out)
	}
	if !hasCommandContaining(t, out, "PreToolUse", "$x\" gate pretooluse") {
		t.Errorf("missing aphrollo pretooluse after migration\n%s", out)
	}
}

// The managed-hook marker must not assume the binary is named "aphrollo": when
// init resolves to a differently-named path (os.Executable in tests, a renamed
// install), re-patching must still recognise and replace its own entries rather
// than append duplicates.
func TestPatchSettings_IdempotentWithRenamedBinary(t *testing.T) {
	t.Parallel()
	const altbin = "/opt/custom/mytool"
	first, _, err := PatchSettings(nil, altbin)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, changed, err := PatchSettings(first, altbin)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Errorf("re-patch with renamed binary changed the file (duplicated hooks)\n%s", second)
	}
	var n int
	for _, c := range commandStrings(t, second, "PreToolUse") {
		if strings.Contains(c, "gate pretooluse") {
			n++
		}
	}
	// One per matcher — the edit tools, Bash and PowerShell. A re-patch that
	// duplicated them would push this past three.
	if n != 3 {
		t.Errorf("expected exactly 3 pretooluse hooks (edit tools, Bash, PowerShell), got %d\n%s", n, second)
	}
}

// Uninstall must also recognise a renamed binary's entries.
func TestStripSettings_RenamedBinary(t *testing.T) {
	t.Parallel()
	installed, _, err := PatchSettings(nil, "/opt/custom/mytool")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, changed, err := StripSettings(installed)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true stripping renamed-binary hooks")
	}
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SessionEnd", "UserPromptSubmit"} {
		if hasCommandContaining(t, out, ev, "tdd ") {
			t.Errorf("%s: renamed-binary tdd entry survived strip\n%s", ev, out)
		}
	}
}

// Uninstall strips every aphrollo tdd entry but leaves foreign hooks intact.
func TestStripSettings_RemovesOnlyManaged(t *testing.T) {
	t.Parallel()
	installed, _, err := PatchSettings([]byte(`{
	  "hooks": {"UserPromptSubmit": [
	    {"hooks": [{"type": "command", "command": "node caveman-mode-tracker.js"}]}
	  ]}
	}`), bin)
	if err != nil {
		t.Fatalf("setup patch: %v", err)
	}
	out, changed, err := StripSettings(installed)
	if err != nil {
		t.Fatalf("StripSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true stripping installed settings")
	}
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SessionEnd", "UserPromptSubmit"} {
		if hasCommandContaining(t, out, ev, "aphrollo tdd") {
			t.Errorf("%s: aphrollo entry survived strip\n%s", ev, out)
		}
	}
	if !hasCommandContaining(t, out, "UserPromptSubmit", "caveman-mode-tracker.js") {
		t.Errorf("strip removed foreign caveman hook\n%s", out)
	}
}

// hookTimeouts returns the "timeout" of every command hook under an event.
func hookTimeouts(t *testing.T, data []byte, event string) []float64 {
	t.Helper()
	var out []float64
	for _, g := range hookGroups(t, data, event) {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if _, isCmd := hm["command"].(string); !isCmd {
				continue
			}
			got, _ := hm["timeout"].(float64)
			out = append(out, got)
		}
	}
	return out
}

// The turn-end hooks end a turn or a task, so the harness must be able to cut
// them off quickly: each carries a short timeout, and a hook the harness kills
// lets the turn end rather than holding it.
func TestPatchSettings_WiresTheTurnEndHooksWithAShortTimeout(t *testing.T) {
	t.Parallel()
	out, _, err := PatchSettings(nil, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	for _, want := range []struct{ event, sub string }{
		{"Stop", "$x\" gate stop"},
		{"SubagentStop", "$x\" gate subagentstop"},
		{"TaskCompleted", "$x\" gate taskcompleted"},
	} {
		if !hasCommandContaining(t, out, want.event, want.sub) {
			t.Errorf("%s: missing command %q\n%s", want.event, want.sub, out)
		}
		timeouts := hookTimeouts(t, out, want.event)
		if len(timeouts) != 1 || timeouts[0] < 1 || timeouts[0] > 10 {
			t.Errorf("%s: timeouts = %v, want one entry of 1 to 10 seconds", want.event, timeouts)
		}
	}
}

// An install that predates the turn-end hooks gets them added beside whatever
// else is on those events, and a second patch finds them present and changes
// nothing.
func TestPatchSettings_AddsTheTurnEndHooksToAnOlderInstallAndSkipsThemOnceThere(t *testing.T) {
	t.Parallel()
	older := []byte(`{
	  "hooks": {
	    "SessionStart": [{"hooks": [{"type": "command", "command": "\"/usr/local/bin/aphrollo\" gate sessionstart", "timeout": 10}]}],
	    "Stop": [{"hooks": [{"type": "command", "command": "node stop-notifier.js"}]}]
	  }
	}`)

	out, changed, err := PatchSettings(older, bin)
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true: the older install lacks the turn-end hooks")
	}
	for _, event := range []string{"Stop", "SubagentStop", "TaskCompleted"} {
		if !hasCommandContaining(t, out, event, "$x\" gate ") {
			t.Errorf("%s: aphrollo hook not added\n%s", event, out)
		}
	}
	if !hasCommandContaining(t, out, "Stop", "stop-notifier.js") {
		t.Errorf("the foreign Stop hook was dropped\n%s", out)
	}

	again, changedAgain, err := PatchSettings(out, bin)
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if changedAgain || string(again) != string(out) {
		t.Errorf("second patch changed settings that already carry the hooks\n%s", again)
	}
}

func TestStripSettings_RemovesTheTurnEndHooksAndKeepsAForeignStopHook(t *testing.T) {
	t.Parallel()
	installed, _, err := PatchSettings([]byte(`{
	  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "node stop-notifier.js"}]}]}
	}`), bin)
	if err != nil {
		t.Fatalf("setup patch: %v", err)
	}

	out, changed, err := StripSettings(installed)
	if err != nil {
		t.Fatalf("StripSettings: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true stripping installed settings")
	}
	for _, event := range []string{"Stop", "SubagentStop", "TaskCompleted"} {
		if hasCommandContaining(t, out, event, "$x\" gate ") {
			t.Errorf("%s: aphrollo entry survived strip\n%s", event, out)
		}
	}
	if !hasCommandContaining(t, out, "Stop", "stop-notifier.js") {
		t.Errorf("strip removed the foreign Stop hook\n%s", out)
	}
}

// userlaunchFakeBin writes a sh script standing in for the aphrollo binary:
// it prints who it is and its arguments.
func userlaunchFakeBin(t *testing.T, path, who string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \""+who+" $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// userlaunchRun runs a hook command the way the harness does, through sh.
func userlaunchRun(t *testing.T, command string) (code int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.WaitDelay = 2 * time.Second
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String(), errb.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, out.String(), errb.String()
}

// The session hooks must pick up the user-space binary an `aphrollo update`
// installed without anyone rewriting settings.json again, and must still run
// the installed path when no user-space binary exists yet.
func TestPatchSettings_HooksRunTheUserSpaceBinaryFirstThenTheInstalledOne(t *testing.T) {
	userlaunchHome(t)
	root, err := userbin.Root()
	if err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(t.TempDir(), "aphrollo"+userbin.ExeSuffix)
	userlaunchFakeBin(t, fallback, "installed")
	out, _, err := PatchSettings(nil, fallback)
	if err != nil {
		t.Fatal(err)
	}
	cmds := commandStrings(t, out, "Stop")
	if len(cmds) != 1 {
		t.Fatalf("Stop commands = %v", cmds)
	}
	if code, so, _ := userlaunchRun(t, cmds[0]); code != 0 || strings.TrimSpace(so) != "installed "+CmdName+" stop" {
		t.Fatalf("before any user-space install: code %d, %q; want the installed binary", code, so)
	}
	userlaunchFakeBin(t, userbin.BinaryPath(root, "4.0.0"), "user")
	if err := userbin.SetCurrent(root, "4.0.0"); err != nil {
		t.Fatal(err)
	}
	if code, so, _ := userlaunchRun(t, cmds[0]); code != 0 || strings.TrimSpace(so) != "user "+CmdName+" stop" {
		t.Fatalf("after: code %d, %q; want the user-space binary", code, so)
	}
}

func TestPatchSettings_AHookWithNoBinaryAnywhereExitsZero(t *testing.T) {
	userlaunchHome(t)
	out, _, err := PatchSettings(nil, filepath.Join(t.TempDir(), "gone"+userbin.ExeSuffix))
	if err != nil {
		t.Fatal(err)
	}
	for _, me := range managedEvents {
		for _, c := range commandStrings(t, out, me.event) {
			if code, so, se := userlaunchRun(t, c); code != 0 || so != "" || !strings.Contains(se, "skipped") {
				t.Fatalf("%s: code %d, stdout %q, stderr %q; want a one-line no-op", me.event, code, so, se)
			}
		}
	}
}

// An install written before the user-space binary existed is rewritten in
// place: the old command goes, a foreign hook stays, and a second pass is a
// no-op.
func TestPatchSettings_RewritesAnOldStyleInstallOnceAndKeepsForeignHooks(t *testing.T) {
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"\"/old/aphrollo\" ` + CmdName + ` stop","timeout":10}]},` +
		`{"hooks":[{"type":"command","command":"echo foreign"}]}]}}`
	out, changed, err := PatchSettings([]byte(old), "/new/aphrollo")
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v; want the old command rewritten", changed, err)
	}
	cmds := commandStrings(t, out, "Stop")
	if len(cmds) != 2 || cmds[0] != "echo foreign" || !strings.Contains(cmds[1], "aphrollo_root=") || strings.Contains(cmds[1], "/old/aphrollo") {
		t.Fatalf("Stop commands = %q; want the foreign hook kept and one launcher command", cmds)
	}
	if _, again, err := PatchSettings(out, "/new/aphrollo"); err != nil || again {
		t.Fatalf("second patch changed = %v, err = %v; want a no-op", again, err)
	}
}

// userlaunchHome gives the test its own account home, so a user-space current
// another test installed is never seen here.
func userlaunchHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
}

// userlaunchShOut runs a shim under sh with a deadline, so a shim that never
// returns fails the test instead of holding the job.
func userlaunchShOut(t *testing.T, shim string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", append([]string{shim}, args...)...)
	cmd.WaitDelay = 2 * time.Second
	return cmd.CombinedOutput()
}
