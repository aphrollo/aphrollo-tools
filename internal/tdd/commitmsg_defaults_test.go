package tdd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitRepo returns the root of a freshly initialized, otherwise empty git
// repository — no aphrollo.toml, no Cargo.toml, nothing that tells this gate
// the repo has ever heard of it. This is the shape a throwaway git init in a
// test's temp dir produces.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		if out, err := git(root, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

// plainRepo is a repo that HAS opted into aphrollo (a root aphrollo.toml, the
// same manifest a Go/Python/Node repo already uses for the undercover flag)
// but sets none of the default checks' own keys — the checks below must
// still apply on their own, since nothing here gates them the way
// `undercover = true` gates the tell-detection layer.
func plainRepo(t *testing.T) string {
	t.Helper()
	root := gitRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\n")
	return root
}

// TestCommitMsg_RejectsAVagueOpenWord pins the vocabulary half of #329's
// subject deny list: an open word that says how much changed, not what.
func TestCommitMsg_RejectsAVagueOpenWord(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	for _, subject := range []string{"fix bug", "wip", "cleanup src dir", "misc changes here"} {
		got := CommitMsg(root, msgFile(t, subject+"\n"))
		if !got.Blocked {
			t.Errorf("subject %q was allowed", subject)
		}
	}
}

// TestCommitMsg_RejectsASubjectUnderFourWords pins the length half of the
// same deny list, independent of vocabulary.
func TestCommitMsg_RejectsASubjectUnderFourWords(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "Rename the helper\n"))
	if !got.Blocked {
		t.Fatal("a three-word subject was allowed")
	}
}

// TestCommitMsg_RejectsAFileListSubject pins the "subject that lists the
// touched files" rule: two or more path-shaped tokens and nothing else.
func TestCommitMsg_RejectsAFileListSubject(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "internal/foo.go and internal/bar.rs\n"))
	if !got.Blocked {
		t.Fatal("a subject naming two files and nothing else was allowed")
	}
}

// TestCommitMsg_AllowsAnOrdinarySubject pins the negative: a normal,
// specific subject must not trip any default check.
func TestCommitMsg_AllowsAnOrdinarySubject(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	for _, subject := range []string{
		"Refuse a commit whose suite never finished",
		"Fix the race in the file watcher's debounce",
		"Rename claudication_test to intermittent_test",
	} {
		got := CommitMsg(root, msgFile(t, subject+"\n"))
		if got.Blocked {
			t.Errorf("ordinary subject %q was rejected: %s", subject, got.Message)
		}
	}
}

// TestCommitMsg_DefaultsApplyWithoutUndercover pins the independence #329
// asks for: these checks are on for every repo, not gated behind the
// undercover opt-in the tell-detection layer needs.
func TestCommitMsg_DefaultsApplyWithoutUndercover(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "wip\n"))
	if !got.Blocked {
		t.Fatal("a vague subject was allowed in a repo that never set undercover")
	}
}

// stageLines writes a file with n lines to root and stages it, so a test can
// put an exact number of changed lines under a commit message.
func stageLines(t *testing.T, root string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := git(root, "add", "big.txt"); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
}

// TestCommitMsg_RequiresABodyWhenTheStagedDiffIsLarge pins the third default
// rule: a big diff with no explanation beyond the subject is rejected.
func TestCommitMsg_RequiresABodyWhenTheStagedDiffIsLarge(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 60)
	got := CommitMsg(root, msgFile(t, "Add the big generated fixture file\n"))
	if !got.Blocked {
		t.Fatal("a 60-line staged diff with no body was allowed")
	}
}

// TestCommitMsg_AllowsAMergeCommitWithALargeDiffAndNoBody pins the merge
// exemption on the body-required rule: git writes the merge subject itself
// and gives the merger no body to begin with, so a routine merge bringing in
// someone else's large diff must not be blocked for lacking prose nobody had
// a chance to write.
func TestCommitMsg_AllowsAMergeCommitWithALargeDiffAndNoBody(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 60)
	got := CommitMsg(root, msgFile(t, "Merge branch 'lane/x'\n"))
	if got.Blocked {
		t.Fatalf("a merge commit with a large diff and no body was rejected: %s", got.Message)
	}
}

// TestCommitMsg_StillRequiresABodyForANonMergeCommitWithTheSameDiff pins the
// other side of the same fix: the merge exemption must not leak into an
// ordinary, hand-authored commit that happens to carry the same large diff.
func TestCommitMsg_StillRequiresABodyForANonMergeCommitWithTheSameDiff(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 60)
	got := CommitMsg(root, msgFile(t, "Add the big generated fixture file\n"))
	if !got.Blocked {
		t.Fatal("a non-merge commit with a large diff and no body was allowed")
	}
}

// TestCommitMsg_ALargeDiffWithAnExplanationIsAllowed pins the other side: a
// body that actually explains the change lets the same diff through.
func TestCommitMsg_ALargeDiffWithAnExplanationIsAllowed(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 60)
	body := "Add the big generated fixture file\n\n" +
		"Regenerated after the schema migration landed, so the golden output\n" +
		"reflects the new field order.\n"
	got := CommitMsg(root, msgFile(t, body))
	if got.Blocked {
		t.Fatalf("a large diff with a real explanation was rejected: %s", got.Message)
	}
}

// TestCommitMsg_ABodyThatIsJustFileNamesDoesNotCount pins the body-quality
// half: a body carrying only file names says nothing more than the subject
// already did, and must not satisfy the requirement.
func TestCommitMsg_ABodyThatIsJustFileNamesDoesNotCount(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 60)
	body := "Add the big generated fixture file\n\ninternal/big.txt and internal/other.txt\n"
	got := CommitMsg(root, msgFile(t, body))
	if !got.Blocked {
		t.Fatal("a body that is itself a file list was accepted as an explanation")
	}
}

// TestCommitMsg_ASmallDiffNeedsNoBody pins the threshold: #329 draws the
// line at 50 changed lines, not at "has no body at all".
func TestCommitMsg_ASmallDiffNeedsNoBody(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	stageLines(t, root, 10)
	got := CommitMsg(root, msgFile(t, "Add a small fixture file for the parser test\n"))
	if got.Blocked {
		t.Fatalf("a 10-line diff was rejected for lacking a body: %s", got.Message)
	}
}

// TestCommitMsg_HonoursTheCommitMessageAllowList pins the escape #329 asks
// for: a repo-declared shape (a release bump) is exempt from the defaults.
func TestCommitMsg_HonoursTheCommitMessageAllowList(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\ncommit-message-allow = [\"^v\\d+\\.\\d+\\.\\d+$\"]\n")
	got := CommitMsg(root, msgFile(t, "v1.2.3\n"))
	if got.Blocked {
		t.Fatalf("an allow-listed release-bump subject was rejected: %s", got.Message)
	}
}

// TestCommitMsg_AllowsATerseSubjectWithNoAphrolloConfig pins the scoping fix:
// a repo that has never told aphrollo it exists — no aphrollo.toml, no
// [workspace.metadata.aphrollo] — is exactly the shape a test helper's
// throwaway `git init` in a temp dir produces, hundreds of times over in this
// repo's own suite, and #329's house style must never reach it.
func TestCommitMsg_AllowsATerseSubjectWithNoAphrolloConfig(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	got := CommitMsg(root, msgFile(t, "wip\n"))
	if got.Blocked {
		t.Fatalf("a terse subject was rejected in a repo with no aphrollo config: %s", got.Message)
	}
}

// TestCommitMsg_RejectsATerseSubjectWithAphrolloConfig pins the other side of
// the same fix: a repo that HAS opted into aphrollo (even with none of the
// default checks' own keys set) keeps #329's rule intact.
func TestCommitMsg_RejectsATerseSubjectWithAphrolloConfig(t *testing.T) {
	t.Parallel()
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "wip\n"))
	if !got.Blocked {
		t.Fatal("a terse subject was allowed in a repo configured for aphrollo")
	}
}
