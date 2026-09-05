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
	return primaryRepoNamed(t, "repo")
}

// primaryRepoNamed behaves like primaryRepo but nests the checkout under a
// directory ending in name, instead of a t.TempDir() basename that is a bare
// sequence number. A test that needs two DISTINGUISHABLE repos — to assert a
// reason string names one and not the other — must call this with two
// different names: a bare sequence number can collide with another test's
// temp dir under -shuffle, making a `strings.Contains` on the raw basename
// answer no stable question.
func primaryRepoNamed(t *testing.T, name string) (primary, linked string) {
	t.Helper()
	primary = filepath.Join(t.TempDir(), name)
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

// A heredoc BODY is data, not shell. `select where x > 5` inside one reads as
// a redirection to a file called `5` if the body is split like a command line,
// and the session is refused a read-only query it never wrote anything with.
func TestPrimaryCheckout_AllowsAHeredocWhoseBodyContainsARedirect(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	reads := []string{
		"cat <<'DOC'\nselect where x > 5\nDOC",
		"psql <<-DOC\n\tselect where x > 5\n\tDOC",
		"cat <<DOC > /dev/null\nx > 5\nDOC",
	}
	for _, cmd := range reads {
		if d := PrimaryCheckoutDecision(bashPayload(t, "b4", primary, cmd)); d.Action != Allow {
			t.Errorf("%q writes nothing into the repo and should Allow, got %+v", cmd, d)
		}
	}
}

// The other half: skipping a heredoc body must not skip the command line that
// opened it, nor the commands that follow the body -- both are where a real
// write sits.
func TestPrimaryCheckout_DeniesAWriteAroundAHeredocBody(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)

	writes := []string{
		"cat > main.go <<'DOC'\npackage main\nDOC",
		"cat <<'DOC' > main.go\npackage main\nDOC",
		"cat <<'DOC'\nnot shell\nDOC\necho hi > notes.txt",
	}
	for _, cmd := range writes {
		if d := PrimaryCheckoutDecision(bashPayload(t, "b5", primary, cmd)); d.Action != Block {
			t.Errorf("%q writes into the primary checkout and should Block, got %+v", cmd, d)
		}
	}
}

// A `cd` out of the primary before the write means the write lands
// somewhere else entirely — the false denial issue #118 evidenced.
func TestPrimaryCheckout_AllowsAWriteAfterCdOutOfThePrimary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, linked := primaryRepo(t)

	cmd := "cd " + shellPath(linked) + " && echo hi > notes.txt"
	if d := PrimaryCheckoutDecision(bashPayload(t, "b6", primary, cmd)); d.Action != Allow {
		t.Fatalf("%q writes into the linked worktree after cd, not the primary, got %+v", cmd, d)
	}
}

// An absolute path INTO the primary is denied even from a worktree's own
// cwd — the false ALLOW issue #118 evidenced, the other half of the same
// bug.
func TestPrimaryCheckout_DeniesAnAbsoluteWriteIntoThePrimaryFromAWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, linked := primaryRepo(t)

	target := filepath.Join(primary, "notes.txt")
	cmd := "echo hi > " + shellPath(target)
	d := PrimaryCheckoutDecision(bashPayload(t, "b7", linked, cmd))
	if d.Action != Block {
		t.Fatalf("%q writes into the primary checkout by absolute path, got %+v", cmd, d)
	}
	if !strings.Contains(d.Reason, "primary checkout is merge-only") {
		t.Fatalf("reason %q must name the rule", d.Reason)
	}
}

// The PowerShell tool is classified exactly like Bash: Set-Content,
// Add-Content and Out-File are its write verbs, and `>`/`>>` its
// redirection.
func TestPrimaryCheckout_ClassifiesPowerShellLikeBash(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, linked := primaryRepo(t)

	writes := []string{
		"Set-Content -Path notes.txt -Value hi",
		"Set-Content notes.txt hi",
		"'hi' | Out-File -FilePath notes.txt",
		"echo hi > notes.txt",
	}
	for _, cmd := range writes {
		if d := PrimaryCheckoutDecision(powerShellPayload(t, "ps1", primary, cmd)); d.Action != Block {
			t.Errorf("PowerShell %q writes into the primary checkout and should Block, got %+v", cmd, d)
		}
	}
	if d := PrimaryCheckoutDecision(powerShellPayload(t, "ps2", linked, "Set-Content -Path notes.txt -Value hi")); d.Action != Allow {
		t.Fatalf("PowerShell in a linked worktree is the happy path, got %+v", d)
	}
}

func TestPrimaryCheckout_AllowsBashInALinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, linked := primaryRepo(t)

	if d := PrimaryCheckoutDecision(bashPayload(t, "b3", linked, "echo hi > notes.txt")); d.Action != Allow {
		t.Fatalf("a shell write inside a linked worktree is the happy path, got %+v", d)
	}
}

// `rm -rf` of a lane dir with no `git worktree prune` leaves the admin entry
// behind under .git/worktrees/, pointing at a directory that no longer
// exists. A repo whose ONLY entry is that stale one has no lane left to
// escape to, so it must not stay merge-only (issue #120).
func TestPrimaryCheckout_AllowsEditWhenTheOnlyWorktreeEntryIsStale(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, linked := primaryRepo(t)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}

	if d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s9")); d.Action != Allow {
		t.Fatalf("a repo whose only worktree entry is stale is an ordinary clone again, got %+v", d)
	}
}

// A stale entry beside a LIVE one must not un-block the primary checkout —
// the rule still holds — but the remedy names the prune fix so an operator
// is not left guessing why the count disagrees with what `git worktree list`
// shows them.
func TestPrimaryMergeOnlyReason_NamesPruneWhenAStaleEntryExists(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)
	stale := filepath.Join(t.TempDir(), "stale-lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/stale", stale)
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}

	d := PrimaryCheckoutDecision(editPayload(t, "Edit", filepath.Join(primary, "main.go"), "s10"))
	if d.Action != Block {
		t.Fatalf("the repo still has a live worktree (lane/x), so the primary stays merge-only, got %+v", d)
	}
	if !strings.Contains(d.Reason, "git worktree prune") {
		t.Fatalf("reason %q must name the prune remedy for the stale entry", d.Reason)
	}
}

// The refusal used to name only the worktree remedy; the session override
// that actually works from inside a turn belongs in the same sentence, under
// its renamed spelling — never the retired "primary-edits" one.
func TestPrimaryRefusal_NamesAllowPrimary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := primaryRepo(t)
	reason := PrimaryMergeOnlyReason(primary)
	if !strings.Contains(reason, "aphrollo gate allow primary") {
		t.Fatalf("reason %q must name `aphrollo gate allow primary`", reason)
	}
	if strings.Contains(reason, "primary-edits") {
		t.Fatalf("reason %q must not name the retired primary-edits spelling", reason)
	}
}

// A session's project directory can be a DIFFERENT repo entirely from the one
// a Bash command writes into — a borld-rooted session `cd`ing into an
// aphrollo-tools worktree, say. The classifier must resolve the repo (and
// name it in the remedy) from the RESOLVED WRITE TARGET, never from the
// session's own project directory (issue 142).
func TestPrimaryCheckout_CrossRepoBashNamesTheWriteTargetsRepoNotTheSessionRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	sessionRepo, _ := primaryRepoNamed(t, "session-repo")          // e.g. the borld primary the session started in
	otherPrimary, otherLinked := primaryRepoNamed(t, "other-repo") // e.g. aphrollo-tools, a wholly different repo

	cmd := "cd " + shellPath(otherLinked) + " && echo hi > notes.txt"
	d := PrimaryCheckoutDecision(bashPayload(t, "bx1", sessionRepo, cmd))
	if d.Action != Allow {
		t.Fatalf("%q writes into another repo's linked worktree, not the session repo's primary, got %+v", cmd, d)
	}

	target := filepath.Join(otherPrimary, "notes.txt")
	cmd2 := "cd " + shellPath(otherLinked) + " && echo hi > " + shellPath(target)
	d2 := PrimaryCheckoutDecision(bashPayload(t, "bx2", sessionRepo, cmd2))
	if d2.Action != Block {
		t.Fatalf("%q writes into the OTHER repo's primary checkout, got %+v", cmd2, d2)
	}
	if !strings.Contains(d2.Reason, filepath.Base(otherPrimary)) {
		t.Fatalf("reason %q must name the repo the command actually targets (%s), not the session's own repo (%s)", d2.Reason, otherPrimary, sessionRepo)
	}
	if strings.Contains(d2.Reason, filepath.Base(sessionRepo)) {
		t.Fatalf("reason %q must not name the session's own repo — the command never touched it", d2.Reason)
	}
}

func TestExistingAncestorDir_WalksUpPastDirectoriesTheWriteWouldCreate(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c")
	if got := existingAncestorDir(deep); got != base {
		t.Fatalf("existingAncestorDir(%q) = %q, want %q", deep, got, base)
	}
	if got := existingAncestorDir(base); got != base {
		t.Fatalf("an existing dir is its own nearest ancestor, got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(base, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := existingAncestorDir(deep); got != filepath.Join(base, "a") {
		t.Fatalf("existingAncestorDir must stop at the deepest existing ancestor, got %q", got)
	}
}
