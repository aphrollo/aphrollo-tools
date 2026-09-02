package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// primaryRepo builds a repo on `main` with one linked worktree, and returns
// the primary checkout and the linked worktree.
func primaryRepo(t *testing.T) (primary, linked string) {
	t.Helper()
	primary = t.TempDir()
	gitInit(t, primary)
	gitDo(t, primary, "checkout", "-q", "-B", "main")
	commitInitial(t, primary)
	linked = filepath.Join(t.TempDir(), "lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", linked)
	return primary, linked
}

func editPayload(t *testing.T, tool, path, session string) []byte {
	t.Helper()
	return preEditJSON(t, tool, path, session)
}

func TestPrimaryCheckout_DeniesEditWhenRepoHasALinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s1"))
	if d.Action != Block {
		t.Fatalf("an edit in the primary checkout should Block, got %+v", d)
	}
	if d.Policy != "primary-checkout" {
		t.Fatalf("policy should name the rule so the deny is counted, got %q", d.Policy)
	}
	// The refusal has to carry the whole recipe: the verb, the branch prefix,
	// the computed worktree path and the base.
	for _, want := range []string{
		"primary checkout is merge-only",
		"git worktree add -b lane/<name>",
		shellPath(filepath.Join(filepath.Dir(primary), ".worktrees", filepath.Base(primary), "<name>")) + " main",
	} {
		if !strings.Contains(d.Reason, want) {
			t.Fatalf("reason %q is missing %q", d.Reason, want)
		}
	}
}

func TestPrimaryCheckout_AllowsEditInALinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, linked := primaryRepo(t)

	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(linked, "main.go"), "s2")); d.Action != Allow {
		t.Fatalf("an edit in a linked worktree is the happy path, got %+v", d)
	}
}

func TestPrimaryCheckout_AllowsEditWhenRepoHasNoLinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	gitDo(t, repo, "checkout", "-q", "-B", "main")
	commitInitial(t, repo)

	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(repo, "main.go"), "s3")); d.Action != Allow {
		t.Fatalf("a repo with no linked worktree is an ordinary clone, got %+v", d)
	}
}

func TestPrimaryCheckout_AllowsEditWhenPrimaryIsNotOnMain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)
	gitDo(t, primary, "checkout", "-q", "-b", "hotfix")

	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s4")); d.Action != Allow {
		t.Fatalf("the rule only holds while the primary checkout carries main, got %+v", d)
	}
}

func TestPrimaryCheckout_EnvEscapeAllowsTheEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(PrimaryEditsEnv, "1")
	primary, _ := primaryRepo(t)

	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s5")); d.Action != Allow {
		t.Fatalf("%s=1 is the stated escape, got %+v", PrimaryEditsEnv, d)
	}
}

func TestPrimaryCheckout_SessionEscapeAllowsTheEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	if msg := tddCommand("primary-edits", "on", "s6", primary); !strings.Contains(msg, "primary") {
		t.Fatalf("/tdd primary-edits on should confirm, got %q", msg)
	}
	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s6")); d.Action != Allow {
		t.Fatalf("the session override is the second stated escape, got %+v", d)
	}
	// Another session is unaffected — the override is per session, like /tdd off.
	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s7")); d.Action != Block {
		t.Fatalf("the override must not leak to another session, got %+v", d)
	}
}

func TestPrimaryCheckout_DeniesEditToAFileInADirectoryThatDoesNotExistYet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	target := filepath.Join(primary, "internal", "brand", "new.go")
	if d := PrimaryCheckoutDecision(editPayload(t, "Write", target, "s8")); d.Action != Block {
		t.Fatalf("a Write creating a new directory still lands in the primary checkout, got %+v", d)
	}
}

func TestPrimaryCheckout_DeniesBashCommandsThatWriteIntoTheRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	writes := []string{
		"echo hi > notes.txt",
		"echo hi >> main.go",
		"sed -i 's/a/b/' main.go",
		"echo hi | tee main.go",
		"cp /etc/hosts main.go",
		"mv other.go main.go",
	}
	for _, cmd := range writes {
		d := PrimaryCheckoutDecision(bashPayload(t, "b1", primary, cmd))
		if d.Action != Block {
			t.Errorf("%q writes into the primary checkout and should Block, got %+v", cmd, d)
		}
	}
}

func TestPrimaryCheckout_AllowsBashCommandsThatWriteNothingInTheRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	reads := []string{
		"git status",
		"go build ./... 2>&1 | head -20",
		"grep -rn foo . > /dev/null",
		"cp main.go /tmp/copy.go",
		"ls -la",
	}
	for _, cmd := range reads {
		d := PrimaryCheckoutDecision(bashPayload(t, "b2", primary, cmd))
		if d.Action != Allow {
			t.Errorf("%q writes nothing into the repo and should Allow, got %+v", cmd, d)
		}
	}
}

func TestPrimaryCheckout_AllowsBashInALinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, linked := primaryRepo(t)

	if d := PrimaryCheckoutDecision(bashPayload(t, "b3", linked, "echo hi > notes.txt")); d.Action != Allow {
		t.Fatalf("a shell write inside a linked worktree is the happy path, got %+v", d)
	}
}

func TestNearestExistingDir_WalksUpPastDirectoriesTheWriteWouldCreate(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c")
	if got := nearestExistingDir(deep); got != base {
		t.Fatalf("nearestExistingDir(%q) = %q, want %q", deep, got, base)
	}
	if got := nearestExistingDir(base); got != base {
		t.Fatalf("an existing dir is its own nearest ancestor, got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(base, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := nearestExistingDir(deep); got != filepath.Join(base, "a") {
		t.Fatalf("nearestExistingDir should stop at the deepest existing ancestor, got %q", got)
	}
}
