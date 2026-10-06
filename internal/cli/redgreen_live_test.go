package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/shfake"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// rglPin points the box's user config at one holding text, so the tdd key is pinned
// (or not) by the test and by nothing on the box.
func rglPin(t *testing.T, text string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	writeFile(t, filepath.Join(dir, "config.toml"), text)
}

// rglLane moves a linked worktree onto a branch the test names, the way a checkout
// of it would: HEAD of the worktree's own git directory.
func rglLane(t *testing.T, dir, branch string) {
	t.Helper()
	gitFile, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(gitFile)), "gitdir:"))
	writeFile(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/"+branch+"\n")
}

// rglRepo is a lane of a repo holding a Go package, a Python project and a node
// project, on a branch whose arm and holdout the test chose (heldOut: the kernel's
// holdout arm for red-green, where the deny is a shadowed guide).
func rglRepo(t *testing.T, arm string, heldOut bool) string {
	t.Helper()
	gateConfigDir(t)
	_, dir := primaryWorktreeRepo(t)
	writeFile(t, filepath.Join(dir, "aphrollo.toml"), "[aphrollo]\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	writeFile(t, filepath.Join(dir, "pkg", "p.go"), "package pkg\n\nvar a = 1\n")
	writeFile(t, filepath.Join(dir, "backend", "pyproject.toml"), "[project]\nname = \"backend\"\n")
	writeFile(t, filepath.Join(dir, "backend", "app", "service.py"), "def f():\n    return 1\n")
	writeFile(t, filepath.Join(dir, "frontend", "package.json"), `{"name": "frontend"}`)
	writeFile(t, filepath.Join(dir, "frontend", "src", "api.ts"), "export const a = 1\n")
	rule, _ := kernel.LookupRule("red-green")
	key := tddarm.RepoKey(dir)
	for i := range 400 {
		lane := fmt.Sprintf("lane/rgl-%d", i)
		if (arm == "" || tddarm.Of(key, lane) == arm) && kernel.InHoldout(lane, rule) == heldOut {
			rglLane(t, dir, lane)
			return dir
		}
	}
	t.Fatalf("no lane in arm %q with holdout %v", arm, heldOut)
	return ""
}

func rglEdit(t *testing.T, session, dir, rel string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Edit", "session_id": session, "cwd": dir, "hook_event_name": "PreToolUse",
		"tool_input": map[string]any{"file_path": filepath.Join(dir, filepath.FromSlash(rel)), "old_string": "a", "new_string": "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type rglAnswer struct {
	code        int
	out, errOut string
}

func rglRun(payload string) rglAnswer {
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, strings.NewReader(payload), &out, &errb)
	return rglAnswer{code, out.String(), errb.String()}
}

// context is the additionalContext of the answer, "" for none.
func (a rglAnswer) context(t *testing.T) string {
	t.Helper()
	if a.out == "" {
		return ""
	}
	var env struct {
		HookSpecificOutput struct {
			AdditionalContext  string `json:"additionalContext"`
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(a.out), &env); err != nil {
		t.Fatalf("answer is no envelope: %v\n%s", err, a.out)
	}
	return env.HookSpecificOutput.AdditionalContext
}

func (a rglAnswer) deny(t *testing.T) (string, bool) {
	t.Helper()
	var env struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if a.out == "" || json.Unmarshal([]byte(a.out), &env) != nil {
		return "", false
	}
	return env.HookSpecificOutput.Reason, env.HookSpecificOutput.PermissionDecision == "deny"
}

// In the warn arm a code edit with no red and no cover is answered with guidance of at
// most 60 tokens that names the unit and the next step, once per unit; it is never denied.
func TestPreToolUse_WarnArmGuidesACodeEditOnceAndNeverDenies(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmWarn, false)
	first := rglRun(rglEdit(t, "rgl-warn", dir, "pkg/p.go"))
	line := first.context(t)
	if first.code != 0 || !strings.Contains(line, "red-green (pkg)") || !strings.Contains(line, "write the failing test first") {
		t.Fatalf("first answer = %+v, want exit 0 and guidance naming unit pkg and the next step", first)
	}
	if n := render.Tokens(len(line)); n > render.CapGuide {
		t.Errorf("guidance is %d tokens, cap %d: %q", n, render.CapGuide, line)
	}
	if _, denied := first.deny(t); denied {
		t.Errorf("a warn-arm answer denied: %s", first.out)
	}
	if second := rglRun(rglEdit(t, "rgl-warn", dir, "pkg/p.go")); second.code != 0 || second.context(t) != "" {
		t.Errorf("second answer = %+v, want silence: one guidance line per unit per lane", second)
	}
}

// In the enforce arm it is a deny of at most 120 tokens that names the override.
func TestPreToolUse_EnforceArmDeniesACodeEditAndNamesTheOverride(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, false)
	for i := range 2 {
		a := rglRun(rglEdit(t, "rgl-enforce", dir, "pkg/p.go"))
		reason, denied := a.deny(t)
		if a.code != 2 || !denied {
			t.Fatalf("answer %d = %+v, want exit 2 and a permission deny", i, a)
		}
		for _, want := range []string{"red-green blocked (pkg)", "write the failing test first", "aphrollo gate allow red-green"} {
			if !strings.Contains(reason, want) {
				t.Errorf("deny %q lacks %q", reason, want)
			}
		}
		if n := render.Tokens(len(reason)); n > render.CapDeny {
			t.Errorf("deny is %d tokens, cap %d: %q", n, render.CapDeny, reason)
		}
	}
}

// The kernel's own holdout arm is a guide where the arm would deny.
func TestPreToolUse_EnforceArmLaneInTheKernelsHoldoutIsOnlyGuided(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, true)
	a := rglRun(rglEdit(t, "rgl-held", dir, "pkg/p.go"))
	if _, denied := a.deny(t); denied || a.code != 0 || !strings.Contains(a.context(t), "red-green (pkg)") {
		t.Errorf("answer = %+v, want guidance and no deny for a lane the holdout shadows", a)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["held_out"] != "true" || got[0].Detail["aphrollo"] != "warn" {
		t.Errorf("records = %+v, want one held-out record of what the hook did: warn", got)
	}
}

// tdd = off does nothing: the answer is the one a plain allowed edit has always had,
// and the record is the shadow's own with no arm.
func TestPreToolUse_OffAnswersAnEditAsItAlwaysHasAndRecordsNoArm(t *testing.T) {
	dir := rglRepo(t, "", false)
	rglPin(t, "tdd = \"off\"\n")
	a := rglRun(rglEdit(t, "rgl-off", dir, "pkg/p.go"))
	if a.code != 0 || a.out != "" || a.errOut != "" {
		t.Errorf("answer = %+v, want exit 0 and no bytes at all", a)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["aphrollo"] != "allow" || got[0].Detail["arm"] != "" || got[0].Detail["tdd"] != "" {
		t.Errorf("records = %+v, want the shadow's own allow, in no arm", got)
	}
	if arms := laneArmEvents(dir); len(arms) != 0 {
		t.Errorf("lane-arm events = %+v, want none while tdd is off", arms)
	}
}

// A pin wins over the arm, and a pinned lane is outside the experiment.
func TestPreToolUse_APinnedModeWinsOverTheLanesArm(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmWarn, false)
	rglPin(t, "tdd = \"enforce\"\n")
	a := rglRun(rglEdit(t, "rgl-pin", dir, "pkg/p.go"))
	if _, denied := a.deny(t); !denied {
		t.Fatalf("answer = %+v, want the pinned enforce to deny in a lane the hash put in warn", a)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["arm"] != "" || got[0].Detail["arm_why"] != "pinned" || got[0].Detail["tdd"] != "enforce" {
		t.Errorf("records = %+v, want a pinned enforce record with no arm", got)
	}
	arms := laneArmEvents(dir)
	if len(arms) != 1 || arms[0].Detail["why"] != "pinned" || arms[0].Detail["arm"] != "" {
		t.Errorf("lane-arm events = %+v, want one, pinned, no arm", arms)
	}
}

func laneArmEvents(root string) []tdd.Event {
	var out []tdd.Event
	for _, e := range tdd.ReadEvents(root) {
		if e.Kind == "lane-arm" {
			out = append(out, e)
		}
	}
	return out
}

// The arm is recorded on the lane once and on every red→green decision.
func TestPreToolUse_RecordsTheArmOnTheLaneOnceAndOnEveryDecision(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmWarn, false)
	rglRun(rglEdit(t, "rgl-rec", dir, "pkg/p.go"))
	rglRun(rglEdit(t, "rgl-rec", dir, "pkg/p.go"))
	arms := laneArmEvents(dir)
	if len(arms) != 1 || arms[0].Detail["arm"] != "warn" || arms[0].Detail["why"] != "assigned" || arms[0].Detail["mode"] != "warn" {
		t.Fatalf("lane-arm events = %+v, want one: the warn arm, assigned", arms)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 2 {
		t.Fatalf("%d red-green records, want 2", len(got))
	}
	for i, e := range got {
		d := e.Detail
		if d["arm"] != "warn" || d["arm_why"] != "assigned" || d["tdd"] != "warn" {
			t.Errorf("record %d = %v, want the warn arm on it", i, d)
		}
	}
	if got[0].Detail["aphrollo"] != "warn" || got[1].Detail["aphrollo"] != "allow" {
		t.Errorf("aphrollo's side = %q then %q, want the one warning then an allow", got[0].Detail["aphrollo"], got[1].Detail["aphrollo"])
	}
}

// An answer that outruns the hook's budget says nothing and is recorded unjudged.
func TestPreToolUse_AnAnswerPastItsBudgetSaysNothingAndIsRecordedUnjudged(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, false)
	old := shadow.LiveBudget
	shadow.LiveBudget = time.Nanosecond
	t.Cleanup(func() { shadow.LiveBudget = old })
	a := rglRun(rglEdit(t, "rgl-late", dir, "pkg/p.go"))
	if a.code != 0 || a.out != "" {
		t.Errorf("answer = %+v, want nothing said", a)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["relation"] != "unjudged" || got[0].Detail["cause"] != "budget" || got[0].Detail["arm"] != "enforce" {
		t.Errorf("records = %+v, want one unjudged for the budget in the enforce arm", got)
	}
}

// The deny's override works: `gate allow red-green` lifts it for the session.
func TestPreToolUse_TheNamedOverrideLiftsTheDenyForTheSession(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, false)
	if a := rglRun(rglEdit(t, "rgl-allow", dir, "pkg/p.go")); a.code != 2 {
		t.Fatalf("control: answer = %+v, want a deny", a)
	}
	if _, err := tdd.AllowWallForSession("rgl-allow", dir, "red-green"); err != nil {
		t.Fatal(err)
	}
	a := rglRun(rglEdit(t, "rgl-allow", dir, "pkg/p.go"))
	if a.code != 0 {
		t.Errorf("answer after the override = %+v, want the edit allowed", a)
	}
}

// A shell write of a code file is asked like an Edit, but the rule table reads red→green
// for a write tool only: the kernel has nothing to say of a command, so the hook says
// nothing, and the record says the kernel allowed it.
func TestPreToolUse_AShellWriteOfACodeFileIsAskedAndTheKernelHasNothingToSayOfIt(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, false)
	a := rglRun(bashPayloadIn(t, dir, "printf x > pkg/p.go"))
	if a.code != 0 || a.out != "" {
		t.Errorf("answer = %+v, want silence for a shell write", a)
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["unit"] != "pkg" || got[0].Detail["trellis"] != "allow" || got[0].Detail["arm"] != "enforce" {
		t.Errorf("records = %+v, want one for unit pkg: the kernel allowing, in the enforce arm", got)
	}
}

// Python and TypeScript units are their projects, named so, in each arm.
func TestPreToolUse_APythonAndATypeScriptEditAreAskedInBothArms(t *testing.T) {
	for _, arm := range []string{tddarm.ArmWarn, tddarm.ArmEnforce} {
		dir := rglRepo(t, arm, false)
		for _, c := range []struct{ rel, unit string }{{"backend/app/service.py", "python:backend"}, {"frontend/src/api.ts", "typescript:frontend"}} {
			a := rglRun(rglEdit(t, "rgl-lang-"+arm, dir, c.rel))
			if arm == tddarm.ArmEnforce {
				reason, denied := a.deny(t)
				if !denied || !strings.Contains(reason, "red-green blocked ("+c.unit+")") {
					t.Errorf("%s %s: answer = %+v, want a deny naming %s", arm, c.rel, a, c.unit)
				}
				continue
			}
			if a.code != 0 || !strings.Contains(a.context(t), "red-green ("+c.unit+")") {
				t.Errorf("%s %s: answer = %+v, want guidance naming %s", arm, c.rel, a, c.unit)
			}
		}
	}
}

// A test file is never asked.
func TestPreToolUse_ATestEditIsNeverGuidedOrDenied(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmEnforce, false)
	if a := rglRun(rglEdit(t, "rgl-test", dir, "pkg/p_test.go")); a.code != 0 || a.out != "" {
		t.Errorf("answer = %+v, want a test edit allowed in silence", a)
	}
}

// rglGitSpawns puts a git on PATH that records each call's argv and runs the real
// one, and answers a reader of what was recorded since the last read.
func rglGitSpawns(t *testing.T) (calls func() []string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available") // skip-ok: git is the subject here, not a dependency that could be faked
	}
	dir, logPath := t.TempDir(), filepath.Join(t.TempDir(), "git-argv.log")
	shfake.Install(t, dir, "git", "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$GITCOUNT_LOG\"\nPATH=$(printf %s \"$PATH\" | sed 's|^[^:]*:||')\nexport PATH\nexec\"$GITCOUNT_REAL\" \"$@\"\n")
	t.Setenv("GITCOUNT_LOG", logPath)
	t.Setenv("GITCOUNT_REAL", real)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		t.Helper()
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		_ = os.Remove(logPath)
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

// The live answer reads the mode, the lane and the record from files: a hook that has the
// rule live spawns no more git than the hook with tdd off does.
func TestPreToolUse_TheLiveRedGreenAnswerSpawnsNoGit(t *testing.T) {
	dir := rglRepo(t, tddarm.ArmWarn, false)
	writeFile(t, filepath.Join(dir, "pkg2", "p.go"), "package pkg2\n\nvar a = 1\n")
	calls := rglGitSpawns(t)
	rglRun(rglEdit(t, "rgl-spawn", dir, "pkg/p.go")) // warm: whatever the hook spawns once
	calls()
	a := rglRun(rglEdit(t, "rgl-spawn", dir, "pkg2/p.go"))
	if !strings.Contains(a.context(t), "red-green (pkg2)") {
		t.Fatalf("setup: the live answer was not given: %+v", a)
	}
	live := calls()
	rglPin(t, tddOffPin)
	rglRun(rglEdit(t, "rgl-spawn", dir, "pkg/p.go"))
	off := calls()
	if len(live) != len(off) {
		t.Errorf("the live hook spawned git %d times, the hook with tdd off %d: %v vs %v", len(live), len(off), live, off)
	}
}
