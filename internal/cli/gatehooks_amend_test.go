package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #749, borld lane/coast-drag: a merge of main into the lane ran the
// whole premerge — suites green — and landed. Amending that merge commit's
// MESSAGE, tree unchanged, was refused by commit-msg: "this message claims
// verification, but no green suite ran against the tree being committed —
// the last precommit verdict for this tree is "ratchet-clean"". The tree was
// byte-identical to the one the premerge had just proven. The verdict belongs
// to the tree, not to whichever hook happened to run last.

// trunkSyncInProgress builds a repo from base, commits lane's files on a
// lane while main moves a file of its own, and leaves `git merge main` in
// progress on the lane with the merged index staged — the state git's
// pre-merge-commit hook sees.
func trunkSyncInProgress(t *testing.T, base, lane map[string]string) string {
	t.Helper()
	isolateGit(t)
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	put := func(files map[string]string) {
		t.Helper()
		for rel, content := range files {
			path := filepath.Join(repo, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	put(base)
	put(map[string]string{"notes.txt": "one\n"})
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-q", "-b", "lane")
	put(lane)
	run("add", ".")
	run("commit", "-qm", "lane work")
	run("checkout", "-q", "main")
	put(map[string]string{"notes.txt": "two\n"})
	run("commit", "-qam", "Move notes")
	run("checkout", "-q", "lane")
	run("merge", "-q", "--no-ff", "--no-commit", "main")
	t.Chdir(repo)
	return repo
}

// goModule is one Go module under dir, its one package in dir/pkg<dir>,
// whose single test passes iff X() returns want. The package directory
// carries the module's name so two modules' suites are two different
// commands.
func goModule(dir string, x, want int) map[string]string {
	pkg := "pkg" + dir
	return map[string]string{
		dir + "/go.mod":                "module " + dir + "\n\ngo 1.21\n",
		dir + "/" + pkg + "/x.go":      fmt.Sprintf("package %s\n\nfunc X() int { return %d }\n", pkg, x),
		dir + "/" + pkg + "/x_test.go": fmt.Sprintf("package %s\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tif X() != %d {\n\t\tt.Fatal(X())\n\t}\n}\n", pkg, want),
	}
}

// commitMsgClaims runs the commit-msg gate over a message claiming the tree
// was verified, against whatever the index holds now.
func commitMsgClaims(t *testing.T) (int, string) {
	t.Helper()
	msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	body := "Merge main into lane\n\nVerified: the merged suite passes on this tree.\n"
	if err := os.WriteFile(msg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	code := Run([]string{"gate", "commitmsg", msg}, strings.NewReader(""), &bytes.Buffer{}, &errb)
	return code, errb.String()
}

func TestCommitMsg_AnAmendOfAMergeKeepsThePremergeGreenForItsUnchangedTree(t *testing.T) {
	gateConfigDir(t)
	repo := trunkSyncInProgress(t, goModule("x", 1, 1), goModule("x", 2, 2))

	var errb bytes.Buffer
	if code := Run([]string{"gate", "premerge"}, strings.NewReader(""), &bytes.Buffer{}, &errb); code != 0 {
		t.Fatalf("premise broken — the merge gate refused the merge: exit %d\n%s", code, errb.String())
	}
	if out, err := fixtureGit("-C", repo, "commit", "-q", "--no-verify", "--no-edit").CombinedOutput(); err != nil {
		t.Fatalf("concluding the merge: %v\n%s", err, out)
	}

	// The amend: commit-msg over an index that is the merge commit's own
	// tree. git runs pre-commit first, in a process of its own; it is not run
	// here, because in this one test process the premerge's "a suite ran
	// green" flag would still be set when it ran and it would stamp the tree
	// itself — vouching for the amend by an accident no real hook shares.
	if code, out := commitMsgClaims(t); code != 0 {
		t.Fatalf("commit-msg refused a claim about the very tree the merge gate ran green: exit %d\n%s", code, out)
	}
}

// The stamp is the merge gate's green, never its verdict: a merge it refused
// — here module a's suite went green and module b's went red — has proven
// nothing about the merged tree, and a claim about that tree stays refused.
func TestCommitMsg_ARefusedMergeLeavesNoGreenForItsTree(t *testing.T) {
	gateConfigDir(t)
	base := goModule("a", 1, 1)
	for k, v := range goModule("b", 1, 1) {
		base[k] = v
	}
	lane := goModule("a", 2, 2)
	for k, v := range goModule("b", 2, 3) {
		lane[k] = v
	}
	trunkSyncInProgress(t, base, lane)

	var errb bytes.Buffer
	if code := Run([]string{"gate", "premerge"}, strings.NewReader(""), &bytes.Buffer{}, &errb); code == 0 {
		t.Fatalf("premise broken — the merge gate allowed a merge whose b suite is red\n%s", errb.String())
	}

	if code, _ := commitMsgClaims(t); code == 0 {
		t.Fatal("commit-msg accepted a verification claim about a merged tree the merge gate refused")
	}
}
