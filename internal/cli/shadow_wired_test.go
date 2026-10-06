package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// shadowEvents are the shadow records the event log holds for root. A
// directory inside a repository has that repository's own log. A directory
// outside any repository shares one log with every other such directory of the
// state root, and a deferred job of an earlier test can append to it after that
// test ended, so there a record counts only when it names root itself.
func shadowEvents(root string) []tdd.Event {
	shared := !insideARepository(root)
	var out []tdd.Event
	for _, e := range tdd.ReadEvents(root) {
		if e.Kind != "shadow" || shared && filepath.Clean(e.Repo) != filepath.Clean(root) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// insideARepository reports whether dir or a parent holds a .git entry.
func insideARepository(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func bashPayloadIn(t *testing.T, cwd, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Bash", "session_id": "cli-shadow", "cwd": cwd, "tool_input": map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRun_PreToolUse_RecordsAShadowEventBesideADiscardDeny(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "git checkout -- f")), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: the shadow record must not change the live decision\nstdout:%s", code, out.String())
	}
	got := shadowEvents(dir)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	d := got[0].Detail
	if d["rule"] != "discard-work" || d["live_rule"] != "discard-bash" || d["trellis"] != "block" || d["aphrollo"] != "block" || d["relation"] != "agree" {
		t.Errorf("shadow detail = %v, want discard-work blocked by both and agreeing", d)
	}
	if got[0].Cmd != "" || strings.Contains(got[0].Verdict+got[0].Stage, "checkout") {
		t.Errorf("a shadow event must carry no command text: %+v", got[0])
	}
}

func TestRun_PreToolUse_ShadowsAWaivedPrimaryWriteAsAWouldBeBlock(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)
	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")

	var out, errb bytes.Buffer
	stdin := strings.NewReader(primaryEditPayload(t, filepath.Join(primary, "main.go")))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0: the waiver lets the write through\nstdout:%s", code, out.String())
	}
	// The write is of a code file, so the red-green rule is asked of it too: its own
	// record is the subject of the tests of that rule.
	got := shadowOfRule(primary, "primary-write")
	if len(got) != 1 {
		t.Fatalf("%d primary-write shadow events, want 1: %+v", len(got), shadowEvents(primary))
	}
	d := got[0].Detail
	if d["rule"] != "primary-write" || d["trellis"] != "block" || d["aphrollo"] != "warn" || d["relation"] != "trellis-stricter" {
		t.Errorf("shadow detail = %v, want primary-write: trellis blocks, aphrollo only warned (the worktree advisory is the call's final answer), trellis-stricter", d)
	}
}

func TestRun_PreToolUse_RecordsNothingForACallNoShadowedRuleReads(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "echo hi")), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:%s", code, out.String())
	}
	if got := shadowEvents(dir); len(got) != 0 {
		t.Errorf("a clean call recorded %d shadow events: %+v", len(got), got)
	}
}

// A shell write is filed under the lane of the checkout it writes into, not the
// lane of the cwd the command ran from: a lane's shell that writes into the
// primary checkout wrote into main.
func TestRun_PreToolUse_ShadowsAShellWriteUnderTheLaneOfItsTarget(t *testing.T) {
	gateConfigDir(t)
	primary, linked := primaryWorktreeRepo(t)
	cmd := "echo hi > " + filepath.ToSlash(filepath.Join(primary, "notes.txt"))

	for _, waived := range []bool{false, true} {
		if waived {
			t.Setenv("APHROLLO_PRIMARY_EDITS", "1")
		}
		var out, errb bytes.Buffer
		Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, linked, cmd)), &out, &errb)
	}
	got := shadowEvents(primary)
	if len(got) != 2 {
		t.Fatalf("%d shadow events, want 2 (blocked, then waived): %+v", len(got), got)
	}
	for i, want := range []struct{ aphrollo, relation string }{{"block", "agree"}, {"allow", "trellis-stricter"}} {
		e := got[i]
		if e.Lane != "main" || filepath.Clean(e.Detail["wt"]) != filepath.Clean(primary) || e.Detail["primary"] != "true" {
			t.Errorf("event %d: lane %q wt %q primary %q, want main, the primary root %q, primary", i, e.Lane, e.Detail["wt"], e.Detail["primary"], primary)
		}
		if e.Detail["aphrollo"] != want.aphrollo || e.Detail["relation"] != want.relation {
			t.Errorf("event %d: aphrollo %q relation %q, want %q %q", i, e.Detail["aphrollo"], e.Detail["relation"], want.aphrollo, want.relation)
		}
	}
}

// Each finding of the law engine is its own fact: a deny finding is the
// deny-law-edit rule and a warn finding the warn-law rule, each naming its law.
func TestPreShadow_LawFindingsAreOneFactEachByTheirOwnSeverity(t *testing.T) {
	p := newPreShadow([]byte(`{"tool_name":"Write"}`))
	p.law([]tdd.LawFinding{{Law: "module_size", Deny: true}, {Law: "doc_path", Deny: false}, {Law: "nan-guard", Deny: true}})
	if len(p.facts) != 3 {
		t.Fatalf("%d facts for 3 findings, want 3: %+v", len(p.facts), p.facts)
	}
	for i, want := range []struct {
		rule, live string
		actual     shadow.Action
	}{
		{"deny-law-edit", "ratchet:module_size", shadow.Block},
		{"warn-law", "ratchet:doc_path", shadow.Warn},
		{"deny-law-edit", "ratchet:nan-guard", shadow.Block},
	} {
		if f := p.facts[i]; f.Rule != want.rule || f.LiveRule != want.live || f.Actual != want.actual {
			t.Errorf("fact %d = %s/%s/%s, want %s/%s/%s", i, f.Rule, f.LiveRule, f.Actual, want.rule, want.live, want.actual)
		}
	}
}

func TestHookTimingEvent_LeavesTheShadowWaitOutOfTheSeconds(t *testing.T) {
	raw := []byte(`{"session_id":"s","agent_id":"a","cwd":"/r"}`)
	e := hookTimingEvent("pretooluse", raw, 3*time.Second, time.Second)
	if e.Secs != 2 || e.Detail["shadow_ms"] != "1000" || e.Detail["hook"] != "pretooluse" || e.Actor != "s/a" {
		t.Errorf("event = %+v, want 2 s of the hook's own, shadow_ms 1000", e)
	}
	if e := hookTimingEvent("stop", raw, 3*time.Second, 0); e.Secs != 3 || e.Detail["shadow_ms"] != "" {
		t.Errorf("no wait: %+v, want 3 s and no shadow_ms", e)
	}
	if e := hookTimingEvent("stop", raw, time.Second, 2*time.Second); e.Secs != 0 {
		t.Errorf("a wait longer than the hook's measured time left %v s, want 0", e.Secs)
	}
}

// stdoutProbe is a stdout that looks at the repo's shadow records at the first
// byte the hook writes: the answer must be out before any record is.
type stdoutProbe struct {
	bytes.Buffer
	root     string
	atFirst  int
	sawFirst bool
}

func (w *stdoutProbe) Write(p []byte) (int, error) {
	if !w.sawFirst {
		w.sawFirst = true
		w.atFirst = len(shadowEvents(w.root))
	}
	return w.Buffer.Write(p)
}

func TestRun_PreToolUse_WritesTheAnswerBeforeTheShadowRecord(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})

	out := &stdoutProbe{root: dir}
	var errb bytes.Buffer
	Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "git checkout -- f")), out, &errb)
	if !out.sawFirst {
		t.Fatal("the hook wrote no answer")
	}
	if out.atFirst != 0 {
		t.Errorf("%d shadow records existed when the answer was first written, want 0: the record follows the answer", out.atFirst)
	}
	if got := len(shadowEvents(dir)); got != 1 {
		t.Errorf("%d shadow records after the hook, want 1", got)
	}
}

func TestRun_PreToolUse_AnswersTheSameBytesWithShadowOnAndOff(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})
	payload := bashPayloadIn(t, dir, "git checkout -- f")

	answer := func(on bool) (int, string, string) {
		old := shadow.Enabled
		shadow.Enabled = on
		t.Cleanup(func() { shadow.Enabled = old })
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "pretooluse"}, strings.NewReader(payload), &out, &errb)
		return code, out.String(), errb.String()
	}
	codeOn, outOn, errOn := answer(true)
	codeOff, outOff, errOff := answer(false)
	if codeOn != codeOff || outOn != outOff || errOn != errOff {
		t.Errorf("shadow on (%d %q %q) and off (%d %q %q) answered differently", codeOn, outOn, errOn, codeOff, outOff, errOff)
	}
	if codeOn != 2 || outOn == "" {
		t.Errorf("the probe call was not a deny: %d %q", codeOn, outOn)
	}
	if got := len(shadowEvents(dir)); got != 1 {
		t.Errorf("%d shadow records over an on run and an off run, want 1 (the on run's)", got)
	}
}

// A waived primary write that a later judgement blocks is recorded as blocked:
// the call's final decision, not the waiver's first.
func TestRun_PreToolUse_ShadowsAWaivedPrimaryWriteByTheCallsFinalDecision(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)
	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")
	b, err := json.Marshal(map[string]any{
		"tool_name": "Write", "session_id": "cli-primary",
		"tool_input": map[string]any{"file_path": filepath.Join(primary, "a_test.go"), "content": "assert x == x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(string(b)), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: the tautology is blocked past the waiver\nstdout:%s", code, out.String())
	}
	got := shadowEvents(primary)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	if d := got[0].Detail; d["rule"] != "primary-write" || d["aphrollo"] != "block" || d["relation"] != "agree" {
		t.Errorf("shadow detail = %v, want primary-write blocked by both: the call's final decision", d)
	}
}

func TestRun_PreToolUse_ShadowsARerunTheRunAlreadyCovered(t *testing.T) {
	gateConfigDir(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module fixture\n\ngo 1.22\n")
	tdd.AppendGateLog("postedit", dir, "go test ./...", "green", 0)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "go test ./...")), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: a whole-suite rerun beside a fresh green\nstdout:%s", code, out.String())
	}
	got := shadowEvents(dir)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	if d := got[0].Detail; d["rule"] != "rerun-suite" || d["live_rule"] != "bash-whole-suite" || d["aphrollo"] != "block" || d["trellis"] != "guide" || d["relation"] != "trellis-softer" {
		t.Errorf("shadow detail = %v, want rerun-suite: aphrollo blocks, trellis only guides", d)
	}
}

func TestRun_PreToolUse_ShadowsATellBranchInAnUndercoverRepo(t *testing.T) {
	gateConfigDir(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	writeFile(t, filepath.Join(dir, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(directPRPayload(t, dir, "git switch -c lane/claude-fix")), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:%s", code, out.String())
	}
	got := shadowEvents(dir)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	if d := got[0].Detail; d["rule"] != "attribution" || d["live_rule"] != "undercover" || d["aphrollo"] != "block" || d["relation"] != "agree" {
		t.Errorf("shadow detail = %v, want attribution blocked by both", d)
	}
	if strings.Contains(fmt.Sprint(got[0]), "claude-fix") {
		t.Errorf("the branch name leaked into the shadow record: %+v", got[0])
	}
}

// denyLawRepo is a repo with one deny law, no-forbidden, over every .txt file.
func denyLawRepo(t *testing.T) string {
	t.Helper()
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})
	writeFile(t, filepath.Join(dir, ".ratchet", "laws", "no-forbidden.toml"), `
name = "no-forbidden"
description = "FORBIDDEN is not allowed"
severity = "deny"
escape = "// forbidden-ok:"
baseline = ".ratchet/baselines/no-forbidden.txt"

[scope]
include = ["**/*.txt"]

[matcher]
kind = "regex-absent"
pattern = "FORBIDDEN"
`)
	writeFile(t, filepath.Join(dir, ".ratchet", "baselines", "no-forbidden.txt"), "")
	return dir
}

func writeNotesPayload(t *testing.T, dir, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Write", "session_id": "cli-law", "cwd": dir,
		"tool_input": map[string]any{"file_path": filepath.Join(dir, "notes.txt"), "content": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A deny law that fires on an edit is a fact of the deny-law-edit rule, named by
// its law, and agrees with the gate's block.
func TestRun_PreToolUse_ShadowsADenyLawFindingOnAnEdit(t *testing.T) {
	gateConfigDir(t)
	dir := denyLawRepo(t)
	b := writeNotesPayload(t, dir, "FORBIDDEN\n")

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(b), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: a deny law blocks the write\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := shadowEvents(dir)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	if d := got[0].Detail; d["rule"] != "deny-law-edit" || d["live_rule"] != "ratchet:no-forbidden" || d["aphrollo"] != "block" || d["relation"] != "agree" {
		t.Errorf("shadow detail = %v, want deny-law-edit by ratchet:no-forbidden, agreed", d)
	}
}

// A law that hits twenty lines is one fact of the call: one event, not twenty.
func TestRun_PreToolUse_AFindingPerHitIsOneShadowEventPerLaw(t *testing.T) {
	gateConfigDir(t)
	dir := denyLawRepo(t)
	b := writeNotesPayload(t, dir, strings.Repeat("FORBIDDEN\n", 20))

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(b), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if got := shadowEvents(dir); len(got) != 1 || got[0].Detail["live_rule"] != "ratchet:no-forbidden" {
		t.Errorf("20 hits of one law wrote %d shadow events %+v, want 1", len(got), got)
	}
}

func TestPreShadow_AFindingRepeatedByHitIsOneFactPerLawAndSeverity(t *testing.T) {
	p := newPreShadow([]byte(`{"tool_name":"Write"}`))
	var found []tdd.LawFinding
	for range 20 {
		found = append(found, tdd.LawFinding{Law: "a", Deny: true})
	}
	found = append(found, tdd.LawFinding{Law: "a", Deny: false}, tdd.LawFinding{Law: "b", Deny: true})
	p.law(found)
	if len(p.facts) != 3 {
		t.Errorf("%d facts, want 3: a as deny, a as warn, b as deny: %+v", len(p.facts), p.facts)
	}
}

// A call with nothing to record starts no record at all: the hook neither spawns the
// writer nor waits for it. A waived call is judged once and is the one that has work.
func TestPreShadow_ACallWithNothingToRecordHasNoWork(t *testing.T) {
	gateConfigDir(t)
	raw := []byte(bashPayloadIn(t, t.TempDir(), "echo hi"))
	t.Setenv("APHROLLO_PRIMARY_EDITS", "")

	p := newPreShadow(raw)
	p.primary = tdd.JudgePrimary(raw)
	if p.hasWork() {
		t.Error("a call that no wall judged and no rule read has work to record")
	}
	shadow.TakeWaited()
	p.record()
	if w := shadow.TakeWaited(); w != 0 {
		t.Errorf("a call with nothing to record waited %v on a record", w)
	}

	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")
	p = newPreShadow(raw)
	p.primary = tdd.JudgePrimary(raw)
	if !p.hasWork() {
		t.Error("a waived call has no work, want its landing resolved inside the record")
	}
}

// Where a waived write lands asks git, which the wall never did for it: the probe
// is begun when the wall judges the call, so the hook's own work overlaps it and
// the record's budget only collects the answer. A call the waiver does not cover
// asks nothing of the record.
func TestPreShadow_AWaivedCallStartsItsLandingProbeWhenTheWallJudgesIt(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)
	raw := []byte(primaryEditPayload(t, filepath.Join(primary, "main.go")))

	t.Setenv("APHROLLO_PRIMARY_EDITS", "")
	p := newPreShadow(raw)
	p.judgePrimary(raw)
	if p.landing != nil {
		t.Error("a call no waiver covers started a landing probe")
	}

	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")
	p = newPreShadow(raw)
	p.judgePrimary(raw)
	if p.landing == nil {
		t.Fatal("a waived call started no landing probe when the wall judged it")
	}
	root, ok := p.landing.Wait(context.Background())
	if !ok || filepath.Clean(root) != filepath.Clean(primary) {
		t.Errorf("landing = %q, %v, want the primary checkout %q", root, ok, primary)
	}
}

// A call the record would not be written for starts no probe: recording off, or a
// payload that names no source.
func TestPreShadow_AWaivedCallStartsNoLandingProbeWhenNothingWillBeRecorded(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)
	raw := []byte(primaryEditPayload(t, filepath.Join(primary, "main.go")))
	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")

	old := shadow.Enabled
	t.Cleanup(func() { shadow.Enabled = old })
	shadow.Enabled = false
	p := newPreShadow(raw)
	p.judgePrimary(raw)
	if p.landing != nil {
		t.Error("a landing probe was started with recording off")
	}
	shadow.Enabled = true

	noSource := []byte(`{"tool_name":"Edit","session_id":"s","tool_input":{"old_string":"a"}}`)
	p = newPreShadow(noSource)
	p.judgePrimary(noSource)
	if p.landing != nil {
		t.Error("a landing probe was started for a payload with no source to file the record under")
	}
}
