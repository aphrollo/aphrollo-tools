package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// offLaneRepo is a repo on lane/x whose managed block is stale (so an ON lane's
// precommit blocks) and whose aphrollo.toml declares the given off share.
func offLaneRepo(t *testing.T, share string) string {
	t.Helper()
	gateConfigDir(t)
	isolateGit(t)
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	put := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	put("CLAUDE.md", "# repo\n\n"+tdd.ClaudeMDBlock(tdd.BlockFlags{}))
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-q", "-b", "lane/x")
	put("aphrollo.toml", "[aphrollo]\nundercover = true\nab-off-share = "+share+"\n")
	run("add", "aphrollo.toml")
	t.Chdir(repo)
	return repo
}

// gateEventKinds are the events a gate or a hook leaves: none may appear for an off lane.
var offLaneForbiddenKinds = []string{"commit_gate", "commit_gate_result", "merge_gate", "merge_gate_result", "stage.timing", "hook.timing", "shadow", "push"}

func assertOffLaneQuiet(t *testing.T, repo, what string, code int, out, errOut string) {
	t.Helper()
	if code != 0 || out != "" || errOut != "" {
		t.Errorf("%s in an off lane: exit %d, stdout %q, stderr %q; want exit 0 and no output", what, code, out, errOut)
	}
	for _, e := range tdd.ReadEvents(repo) {
		if slices.Contains(offLaneForbiddenKinds, e.Kind) {
			t.Errorf("%s in an off lane left a %q event: %+v", what, e.Kind, e)
		}
	}
}

// driveOffEntry runs one entry point of the table the way git or the harness would.
func driveOffEntry(t *testing.T, name, repo string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	switch name {
	case "sessionstart", "pretooluse", "posttooluse", "posttoolusefailure", "userpromptsubmit", "sessionend", "stop", "subagentstop", "taskcompleted":
		t.Cleanup(func() { tdd.EnableDeferredPhases(false) })
		code = runGate([]string{name}, strings.NewReader(offPayloadFor(t, name, "sess-offlane", repo)), &o, &e)
	case "guardrail":
		code = Run([]string{"guardrail", "pretooluse"}, strings.NewReader(offPayloadFor(t, "pretooluse", "sess-offlane", repo)), &o, &e)
	case "precommit", "premerge", "premergecommit", "postcommit", "postmerge", "postrewrite":
		code = Run([]string{"gate", name}, strings.NewReader(""), &o, &e)
	case "prepush":
		empty, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		defer empty.Close()
		old := os.Stdin
		os.Stdin = empty
		defer func() { os.Stdin = old }()
		code = Run([]string{"gate", name}, strings.NewReader(""), &o, &e)
	case "commitmsg":
		msg := filepath.Join(t.TempDir(), "MSG")
		if err := os.WriteFile(msg, []byte("fix\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		code = Run([]string{"gate", "commitmsg", msg}, strings.NewReader(""), &o, &e)
	default:
		t.Fatalf("offLaneEntries has a row %q that this test cannot drive: add its driver", name)
	}
	return code, o.String(), e.String()
}

// Every entry point of the table, driven with an off lane: the silent ones answer
// nothing, decide nothing, run no suite and leave no gate event. A hook added
// without a row fails the dispatcher; a row added without a driver fails here.
func TestOffLane_EveryEntryPointOfTheTableIsSilent(t *testing.T) {
	names := make([]string, 0, len(offLaneEntries))
	for name := range offLaneEntries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if offLaneEntries[name].How != laneSilent {
			continue
		}
		t.Run(name, func(t *testing.T) {
			repo := offLaneRepo(t, "100")
			code, out, errOut := driveOffEntry(t, name, repo)
			assertOffLaneQuiet(t, repo, name, code, out, errOut)
		})
	}
}

// The control: the same repo and the same entry points with no off share speak, so the
// silence above is the off lane's, not the fixture's.
func TestOffLane_TheSameEntryPointsSpeakWhenTheShareIsZero(t *testing.T) {
	repo := offLaneRepo(t, "0")
	code, _, errOut := driveOffEntry(t, "precommit", repo)
	if code != 1 || !strings.Contains(errOut, "managed-block-stale") {
		t.Errorf("precommit on an on lane: exit %d, stderr %q; want the stale-managed-block refusal", code, errOut)
	}
	code, out, _ := driveOffEntry(t, "pretooluse", repo)
	if code == 0 && out == "" {
		t.Errorf("pretooluse (git reset --hard) on an on lane said nothing; want the discard wall")
	}
}

// Every session hook of the dispatcher has a row, and so does every git hook the
// gate accepts: the table is the list a new entry point must join.
func TestOffLane_EverySessionHookAndGitHookHasARow(t *testing.T) {
	for hook := range sessionHooks {
		if _, ok := offLaneEntries[hook]; !ok {
			t.Errorf("session hook %q has no row in offLaneEntries", hook)
		}
	}
	for _, hook := range gitHooks {
		if _, ok := offLaneEntries[hook]; !ok {
			t.Errorf("git hook %q has no row in offLaneEntries", hook)
		}
	}
}

// An off lane's commit is a lifecycle event: the time and the sha, and nothing else of the
// gate's.
func TestOffLane_PostcommitRecordsTheCommitAndNothingElse(t *testing.T) {
	repo := offLaneRepo(t, "100")
	if out, err := fixtureGit("-C", repo, "commit", "-qm", "work", "--no-verify").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	code, out, errOut := driveOffEntry(t, "postcommit", repo)
	assertOffLaneQuiet(t, repo, "postcommit", code, out, errOut)
	commits := eventsOfKind(tdd.ReadEvents(repo), "commit")
	if len(commits) != 1 || len(commits[0].Detail["sha"]) != 40 {
		t.Fatalf("commit events = %+v, want one carrying the 40-character sha", commits)
	}
	if arms := eventsOfKind(tdd.ReadEvents(repo), "lane-arm"); len(arms) != 1 || arms[0].Detail["mode"] != "off" {
		t.Errorf("lane-arm events = %+v, want one with mode off", arms)
	}
}

// A hook of an off lane records the lane's arm once, however many hooks fire.
func TestOffLane_AHookRecordsTheLaneArmOnce(t *testing.T) {
	repo := offLaneRepo(t, "100")
	for range 3 {
		driveOffEntry(t, "pretooluse", repo)
	}
	arms := eventsOfKind(tdd.ReadEvents(repo), "lane-arm")
	if len(arms) != 1 || arms[0].Detail["mode"] != "off" || arms[0].Detail["arm"] != "off" {
		t.Errorf("lane-arm events = %+v, want exactly one with mode off", arms)
	}
}

// The commit-msg gate of an off lane keeps only the undercover layer: the house style
// (a subject of four words, a body) is a gate of aphrollo's, and is off.
func TestOffLane_CommitmsgKeepsTheUndercoverLayerAndDropsHouseStyle(t *testing.T) {
	repo := offLaneRepo(t, "100")
	msg := filepath.Join(t.TempDir(), "MSG")
	write := func(s string) {
		if err := os.WriteFile(msg, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fix\n")
	var o, e bytes.Buffer
	if code := Run([]string{"gate", "commitmsg", msg}, strings.NewReader(""), &o, &e); code != 0 {
		t.Errorf("house-style message in an off lane: exit %d, %q; want it passed", code, e.String())
	}
	write("Fix the parser\n\nCo-Authored-By: someone <a@b.c>\nGenerated with Claude Code\n")
	e.Reset()
	if code := Run([]string{"gate", "commitmsg", msg}, strings.NewReader(""), &o, &e); code != 1 {
		t.Errorf("undercover tell in an off lane: exit %d, %q; want it still refused", code, e.String())
	}
	_ = repo
}

// The hook's cwd decides which lane it serves; the edited file's directory is only the
// fallback when the payload names no cwd.
func TestOffLane_TheHooksCwdDecidesBeforeTheEditedFilesDirectory(t *testing.T) {
	repo := offLaneRepo(t, "100")
	elsewhere := t.TempDir()
	withCwd := offPayload(t, map[string]any{"session_id": "s", "cwd": repo, "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(elsewhere, "x.go")}})
	if !offLaneHook([]byte(withCwd)) {
		t.Errorf("a hook whose cwd is an off lane was judged by the directory of its file instead")
	}
	noCwd := offPayload(t, map[string]any{"session_id": "s", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(repo, "x.go")}})
	if !offLaneHook([]byte(noCwd)) {
		t.Errorf("a hook with no cwd did not fall back to its file's directory")
	}
}

// Mid-rebase HEAD is detached, and the off lane stays off: silent, its commits in its lane.
func TestOffLane_StaysSilentMidRebase(t *testing.T) {
	repo := offLaneRepo(t, "100")
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "rebase-merge", "head-name"), []byte("refs/heads/lane/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = head
	code, out, errOut := driveOffEntry(t, "precommit", repo)
	assertOffLaneQuiet(t, repo, "precommit mid-rebase", code, out, errOut)
}

// A lane is recorded at first sight, on or off, because the record is what keeps it in its
// arm when the share changes later.
func TestOffLane_AnOnLanesFirstHookRecordsItsArmAndAShareRaisedLaterDoesNotFlipIt(t *testing.T) {
	repo := offLaneRepo(t, "0")
	driveOffEntry(t, "pretooluse", repo)
	arms := eventsOfKind(tdd.ReadEvents(repo), "lane-arm")
	if len(arms) != 1 || arms[0].Detail["mode"] == "off" {
		t.Fatalf("lane-arm events = %+v, want one for an on lane", arms)
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\nundercover = true\nab-off-share = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if laneOffAt(repo) {
		t.Errorf("a lane recorded as on went off when the share was raised")
	}
}
