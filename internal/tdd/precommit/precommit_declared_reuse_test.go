package precommit

import (
	"strings"
	"testing"
	"time"
)

const dreuseDeclared = "[aphrollo.precommit]\n\".\" = [{ argv = [\"git\", \"--version\"], inputs = [\"web/**\"] }]\n"

const dreuseUndeclared = "[aphrollo.precommit]\n\".\" = [[\"git\", \"--version\"]]\n"

// dreuseRunner answers every run green except the declared command, which it
// counts and answers with pass and dur.
func dreuseRunner(declaredRuns *int, pass bool, dur time.Duration) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		if r.Cmd == "git" {
			*declaredRuns++
			return SuiteResult{Passed: pass, Duration: dur}
		}
		return SuiteResult{Passed: true}
	}
}

// dreuseLane is a Go repo whose lane touched web/x.ts and was judged by the
// commit gate with the runner given, then committed; trunk then moved by
// trunkFiles. The merge of the lane into trunk is left uncommitted, the state
// the merge gate fires in.
func dreuseLane(t *testing.T, toml string, laneRun SuiteRunner, trunkFiles map[string]string) string {
	t.Helper()
	root := makeGoRepo(t)
	trunk := currentBranch(t, root)
	write(t, root, "aphrollo.toml", toml)
	write(t, root, "web/x.ts", "export const x = 1\n")
	write(t, root, "web/z.ts", "export const z = 1\n")
	write(t, root, "docs/a.md", "# a\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	gitDo(t, root, "checkout", "-qb", "lane/work")
	write(t, root, "web/x.ts", "export const x = 2\n")
	write(t, root, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	Precommit(root, laneRun)
	gitDo(t, root, "commit", "-qm", "lane change")
	gitDo(t, root, "checkout", "-q", trunk)
	for rel, body := range trunkFiles {
		write(t, root, rel, body)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk change")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/work")
	return root
}

// A trunk that moved only under docs/ changes nothing the declared command
// reads, so the merge reuses the lane's green instead of running it again.
func TestDeclaredReuse_MergeSkipsACommandWhoseInputsDidNotMove(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, 90*time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	if laneRuns != 1 {
		t.Fatalf("the commit gate ran the declared command %d times, want 1", laneRuns)
	}
	if res := Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if mergeRuns != 0 {
		t.Fatalf("the merge ran the declared command %d times with its inputs unmoved, want 0", mergeRuns)
	}
}

// A trunk that moved a file under the declared inputs makes the lane's green a
// fact about another tree: the merge runs the command.
func TestDeclaredReuse_MergeRunsACommandWhoseInputsMoved(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, 90*time.Second),
		map[string]string{"web/z.ts": "export const z = 2\n"})
	if res := Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if mergeRuns != 1 {
		t.Fatalf("the merge ran the declared command %d times with its inputs moved, want 1", mergeRuns)
	}
}

// A command that declares no inputs is never reused: default off.
func TestDeclaredReuse_NoDeclaredInputsNeverReuses(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseUndeclared, dreuseRunner(&laneRuns, true, 90*time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	if res := Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if mergeRuns != 1 {
		t.Fatalf("the merge ran a command with no declared inputs %d times, want 1", mergeRuns)
	}
}

// A lane whose commit gate saw the command fail left a red entry, and a red
// entry is never a reason to skip the command.
func TestDeclaredReuse_ARedLaneEntryNeverReuses(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, false, 90*time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	if res := Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if mergeRuns != 1 {
		t.Fatalf("the merge ran the command %d times after a red lane entry, want 1", mergeRuns)
	}
}

// A reuse says so on the gate log, with the seconds the lane's green took.
func TestDeclaredReuse_ReuseIsLoudAndRecordsTheSavedSeconds(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, 90*time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	if res := Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	var got []string
	for _, e := range ReadEvents(root) {
		if strings.HasPrefix(e.Verdict, "declared-reuse") {
			got = append(got, e.Verdict+" saved="+e.Detail["saved_secs"])
		}
	}
	if len(got) != 1 || got[0] != "declared-reuse saved=90" {
		t.Fatalf("reuse events = %q, want [\"declared-reuse saved=90\"]", got)
	}
}

// inputs is a key of the inline table, read as the globs it names.
func TestDeclaredReuse_InputsIsAKeyOfTheInlineTable(t *testing.T) {
	t.Parallel()
	cmds, err := parseDeclaredCommands(`[{ argv = ["git", "--version"], inputs = ["web/**", "package.json"] }]`)
	if err != nil {
		t.Fatalf("inputs was refused: %v", err)
	}
	if got := strings.Join(cmds[0].Inputs, ","); got != "web/**,package.json" {
		t.Fatalf("inputs = %q, want web/**,package.json", got)
	}
}
