package precommit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

var dmissCmd = declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**"}}

// dmissStore is an empty store path for one test.
func dmissStore(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "declared-verdicts.json")
}

// dmissRecord stores what a judged run of c over root's tree left, in the store
// at path.
func dmissRecord(t *testing.T, path, root string, c declaredCommand, green bool) {
	t.Helper()
	k := declaredKeying(root, c)
	if k.err != nil {
		t.Fatalf("no key to record under: %v", k.err)
	}
	recordDeclaredVerdictAt(path, k.key, declaredVerdictFrom(k, green, 5), declaredVerdictsMax)
}

func dmissWant(t *testing.T, path, root string, c declaredCommand, want string) {
	t.Helper()
	if got := declaredMissReasonAt(path, root, c); got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

func TestDeclaredMiss_NoRecordedVerdictForTheTree(t *testing.T) {
	t.Parallel()
	dmissWant(t, dmissStore(t), dreuseKeyRepo(t), dmissCmd, "no recorded verdict for this tree")
}

func TestDeclaredMiss_ARedVerdictIsSaidToBeRed(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	dmissRecord(t, path, root, dmissCmd, false)
	dmissWant(t, path, root, dmissCmd, "the recorded verdict was red")
}

func TestDeclaredMiss_ChangedInputsAreNamed(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	dmissRecord(t, path, root, dmissCmd, true)
	write(t, root, "web/x.ts", "export const x = 2\n")
	dmissWant(t, path, root, dmissCmd, "inputs changed")
}

func TestDeclaredMiss_AChangedLockfileIsNamed(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	dmissRecord(t, path, root, dmissCmd, true)
	write(t, root, "package-lock.json", "{}\n")
	dmissWant(t, path, root, dmissCmd, "lockfile/manifest changed")
}

func TestDeclaredMiss_AChangedToolIsNamed(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	tool := filepath.Join(t.TempDir(), "lint-tool.exe")
	if err := os.WriteFile(tool, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := declaredCommand{Argv: []string{tool}, Inputs: []string{"web/**"}}
	dmissRecord(t, path, root, c, true)
	if err := os.WriteFile(tool, []byte("version two"), 0o755); err != nil {
		t.Fatal(err)
	}
	dmissWant(t, path, root, c, "tool changed")
}

func TestDeclaredMiss_AGlobMatchingNoFileIsNamed(t *testing.T) {
	t.Parallel()
	c := declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**", "nothing/**"}}
	dmissWant(t, dmissStore(t), dreuseKeyRepo(t), c, "a glob matched no file (nothing/**)")
}

func TestDeclaredMiss_AGlobSelectingAnIgnoredFileNamesIt(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	write(t, root, ".gitignore", "web/gen/\n")
	write(t, root, "web/gen/a.ts", "export const a = 1\n")
	dmissWant(t, dmissStore(t), root, dmissCmd, "a glob selects an ignored file (web/gen/a.ts)")
}

func TestDeclaredMiss_AGlobLeavingTheRootIsNamed(t *testing.T) {
	t.Parallel()
	c := declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**", "../x/**"}}
	dmissWant(t, dmissStore(t), dreuseKeyRepo(t), c, "a glob leaves the root")
}

func TestDeclaredMiss_AToolThatCannotBeFoundIsNamed(t *testing.T) {
	t.Parallel()
	c := declaredCommand{Argv: []string{"no-such-tool-for-reuse"}, Inputs: []string{"web/**"}}
	dmissWant(t, dmissStore(t), dreuseKeyRepo(t), c, "the tool could not be found (no-such-tool-for-reuse)")
}

func TestDeclaredMiss_AStoreThatCannotBeReadIsNamed(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	newer := dmissStore(t)
	if err := os.WriteFile(newer, []byte(`{"schema": 999, "verdicts": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dmissWant(t, newer, root, dmissCmd, "the store could not be read")
	corrupt := dmissStore(t)
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	dmissWant(t, corrupt, root, dmissCmd, "the store could not be read")
}

// An entry written before the component hashes were kept says nothing about
// which part moved: it reads as no record.
func TestDeclaredMiss_AnEntryWithoutComponentHashesIsNoRecord(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	recordDeclaredVerdictAt(path, "old-key", declaredVerdict{Green: true, Secs: 5}, declaredVerdictsMax)
	dmissWant(t, path, root, dmissCmd, "no recorded verdict for this tree")
}

// The comparison is against the most recent entry for the same command and
// root, so an older green over other inputs does not name the cause.
func TestDeclaredMiss_ComparesAgainstTheMostRecentEntry(t *testing.T) {
	t.Parallel()
	path, root := dmissStore(t), dreuseKeyRepo(t)
	k := declaredKeying(root, dmissCmd)
	old := declaredVerdictFrom(k, true, 5)
	old.At = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	old.Parts.Locks = "other-locks"
	recordDeclaredVerdictAt(path, "old-key", old, declaredVerdictsMax)
	newer := declaredVerdictFrom(k, true, 5)
	newer.At = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	recordDeclaredVerdictAt(path, "new-key", newer, declaredVerdictsMax)
	write(t, root, "web/x.ts", "export const x = 2\n")
	dmissWant(t, path, root, dmissCmd, "inputs changed")
}

// The merge gate says why it ran a declared command instead of reusing it.
func TestDeclaredMiss_TheMergeGatePrintsTheReasonLine(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, time.Second),
		map[string]string{"web/z.ts": "export const z = 2\n"})
	out := dmissStderr(t, root, func() { Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)) })
	want := "[run] git --version: no reuse — inputs changed\n"
	if !strings.Contains(out, want) {
		t.Errorf("merge stderr lacks %q:\n%s", want, out)
	}
}

func TestDeclaredMiss_AReuseSaysNothingOfAMiss(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	out := dmissStderr(t, root, func() { Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)) })
	if strings.Contains(out, "no reuse") {
		t.Errorf("a reused command printed a miss:\n%s", out)
	}
}

// The commit gate that cannot key a declared command says so: a silent miss
// here is what left the merge with nothing to reuse.
func TestDeclaredMiss_TheCommitGateSaysWhenItCannotRecord(t *testing.T) {
	t.Parallel()
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo.precommit]\n\".\" = [{ argv = [\"git\", \"--version\"], inputs = [\"nothing/**\"] }]\n")
	write(t, root, "web/x.ts", "export const x = 1\n")
	write(t, root, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	var runs int
	out := dmissStderr(t, root, func() { Precommit(root, dreuseRunner(&runs, true, time.Second)) })
	want := "[note] git --version: not recorded for reuse — a glob matched no file (nothing/**)\n"
	if !strings.Contains(out, want) {
		t.Errorf("commit stderr lacks %q:\n%s", want, out)
	}
}

// Merge N+1 reuses merge N's green when the inputs did not move: the merge
// gate records after a real run, as the commit gate does.
func TestDeclaredMiss_AMergeRunIsRecordedForTheNextMerge(t *testing.T) {
	t.Parallel()
	var laneRuns, first, second int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, time.Second),
		map[string]string{"web/z.ts": "export const z = 2\n"})
	if res := Mechanical(root, dreuseRunner(&first, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if first != 1 {
		t.Fatalf("the first merge ran the command %d times, want 1 (its inputs moved)", first)
	}
	if res := Mechanical(root, dreuseRunner(&second, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if second != 0 {
		t.Fatalf("the next merge over the same inputs ran the command %d times, want 0", second)
	}
}

// A red merge run is recorded as red and is not reused.
func TestDeclaredMiss_ARedMergeRunIsNotReused(t *testing.T) {
	t.Parallel()
	var laneRuns, first, second int
	root := dreuseLane(t, dreuseDeclared, dreuseRunner(&laneRuns, true, time.Second),
		map[string]string{"web/z.ts": "export const z = 2\n"})
	Mechanical(root, dreuseRunner(&first, false, time.Second))
	Mechanical(root, dreuseRunner(&second, true, time.Second))
	if second != 1 {
		t.Fatalf("the merge after a red ran the command %d times, want 1", second)
	}
}

// The store is shared by every worktree of a repo: the key a lane's checkout
// computes is the one the merge gate's checkout computes.
func TestDeclaredMiss_AnotherWorktreeOfTheRepoComputesTheSameKey(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	write(t, root, "scripts/lint.sh", "#!/bin/sh\nexit 0\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "files")
	wt := filepath.Join(t.TempDir(), "merge-wt")
	gitDo(t, root, "worktree", "add", "-q", "-b", "gate-prmerge-warm", wt)
	for _, c := range []declaredCommand{dmissCmd, {Argv: []string{"./scripts/lint.sh"}, Inputs: []string{"web/**"}}} {
		inLane := declaredKeying(root, c)
		if inLane.err != nil {
			t.Fatalf("%v: no key in the lane: %v", c.Argv, inLane.err)
		}
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(filepath.Join(wt, "scripts", "lint.sh"), later, later); err != nil {
			t.Fatal(err)
		}
		inMerge := declaredKeying(wt, c)
		if inMerge.err != nil || inMerge.key != inLane.key {
			t.Errorf("%v: key in the other worktree = %q (err %v), want the lane's %q", c.Argv, inMerge.key, inMerge.err, inLane.key)
		}
	}
	write(t, root, "scripts/lint.sh", "#!/bin/sh\nexit 1\n")
	if changed := declaredKeying(root, declaredCommand{Argv: []string{"./scripts/lint.sh"}, Inputs: []string{"web/**"}}); changed.err != nil || changed.key == declaredKeying(wt, declaredCommand{Argv: []string{"./scripts/lint.sh"}, Inputs: []string{"web/**"}}).key {
		t.Errorf("editing the script left the key unchanged (err %v)", changed.err)
	}
}

// dmissStderr is what the gate says on stderr about root while fn runs, from
// a sink of root's own so parallel tests do not hear one another.
func dmissStderr(t *testing.T, root string, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	restore := rootseam.SetStderr(root, &buf)
	defer restore()
	fn()
	return buf.String()
}
