package precommit

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// A command judged against HEAD fails only over findings HEAD does not have.
// Run on a tree whose inputs, lockfiles and tool equal HEAD's, it prints what
// HEAD's run prints, so it cannot fail: the gate skips it, and compares the
// two from git's objects without a checkout.

// dlinesGo is the Go change that makes the commit gate run the root's checks
// at all: a change to docs alone takes its fast path.
const dlinesGo = "package a\n\nfunc A() int { return 1 }\n"

const dlinesToml = "[aphrollo.precommit]\n\".\" = [{ argv = [\"git\", \"--version\"], inputs = [\"web/**\"], baseline = \"lines\" }]\n"

// dlinesRepo is a committed Go repo declaring dlinesToml, with an input under
// web/ and a doc.
func dlinesRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", dlinesToml)
	write(t, root, "web/x.ts", "export const x = 1\n")
	write(t, root, "docs/a.md", "# a\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// dlinesCommit stages the change and runs the commit gate over it, answering
// how often the declared command ran and what the gate said on stderr.
func dlinesCommit(t *testing.T, root string, files map[string]string) (runs int, stderr string) {
	t.Helper()
	for rel, body := range files {
		write(t, root, rel, body)
	}
	gitDo(t, root, "add", "-A")
	sink := &dparSink{changed: make(chan struct{}, 1)}
	t.Cleanup(rootseam.SetStderr(root, sink))
	if res := Precommit(root, dreuseRunner(&runs, true, time.Second)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	return runs, sink.String()
}

// A lane that touches nothing the command reads is not run, and the gate says
// the tree equals the base's.
func TestDeclaredLines_ALaneThatDoesNotTouchTheInputsSkipsTheCommand(t *testing.T) {
	t.Parallel()
	root := dlinesRepo(t)
	runs, out := dlinesCommit(t, root, map[string]string{"docs/a.md": "# a, moved\n", "internal/a/a.go": dlinesGo})
	want := "[reuse] git --version: inputs equal the base's — no new findings possible ("
	if runs != 0 || !strings.Contains(out, want) {
		t.Fatalf("ran %d times, stderr lacks %q:\n%s", runs, want, out)
	}
}

// A lane that changes a file the command reads gets it run: the findings may
// be new.
func TestDeclaredLines_ALaneThatChangesAnInputRunsTheCommand(t *testing.T) {
	t.Parallel()
	root := dlinesRepo(t)
	runs, out := dlinesCommit(t, root, map[string]string{"web/x.ts": "export const x = 2\n", "internal/a/a.go": dlinesGo})
	if runs != 1 || strings.Contains(out, "[reuse]") {
		t.Fatalf("ran %d times (want 1) or reused:\n%s", runs, out)
	}
}

// A lockfile or manifest move changes what the tool resolves with no input
// touched: the command runs.
func TestDeclaredLines_ALockfileChangeRunsTheCommand(t *testing.T) {
	t.Parallel()
	root := dlinesRepo(t)
	runs, _ := dlinesCommit(t, root, map[string]string{"package-lock.json": "{}\n", "internal/a/a.go": dlinesGo})
	if runs != 1 {
		t.Fatalf("ran %d times after a lockfile appeared, want 1", runs)
	}
}

// Any doubt runs the command: a base that cannot be read, a glob that matches
// nothing at the base or on the tree, a tool that cannot be found, a glob
// outside the root.
func TestDeclaredLines_AnyDoubtRunsTheCommand(t *testing.T) {
	t.Parallel()
	noCommit := t.TempDir()
	gitInit(t, noCommit)
	write(t, noCommit, "web/x.ts", "export const x = 1\n")
	based := dlinesRepo(t)
	write(t, based, "other/y.ts", "export const y = 1\n")
	cases := []struct {
		name, root string
		c          declaredCommand
		want       string
	}{
		{"the base cannot be read", noCommit, declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**"}}, "the base could not be compared"},
		{"a glob matches nothing at the base", based, declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**", "other/**"}}, "a glob matches no file at the base"},
		{"a glob matches nothing on the tree", based, declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"nope/**"}}, "a glob matched no file"},
		{"the tool cannot be found", based, declaredCommand{Argv: []string{"no-such-tool-x"}, Inputs: []string{"web/**"}}, "the tool could not be found"},
		{"a glob leaves the root", based, declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"../x"}}, "a glob leaves the root"},
	}
	for _, tc := range cases {
		b := linesBaseCompare(tc.root, tc.c)
		if b.equal || !strings.Contains(b.why, tc.want) {
			t.Errorf("%s: equal=%v why=%q, want a run saying %q", tc.name, b.equal, b.why, tc.want)
		}
	}
}

// At the merge a command that differs from the base says why it runs.
func TestDeclaredLines_TheMergeNamesWhatDiffersFromTheBase(t *testing.T) {
	t.Parallel()
	var laneRuns, mergeRuns int
	root := dreuseLane(t, dlinesToml, dreuseRunner(&laneRuns, true, time.Second),
		map[string]string{"docs/a.md": "# a, moved\n"})
	out := dmissStderr(t, root, func() { Mechanical(root, dreuseRunner(&mergeRuns, true, time.Second)) })
	want := "[run] git --version: no reuse — inputs differ from the base (inputs)\n"
	if mergeRuns != 1 || !strings.Contains(out, want) {
		t.Fatalf("merge ran %d times, stderr lacks %q:\n%s", mergeRuns, want, out)
	}
}
