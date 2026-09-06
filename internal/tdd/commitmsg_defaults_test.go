package tdd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// plainRepo is a repo with no [workspace.metadata.aphrollo] at all — the
// default checks must still apply, since they are not gated behind
// undercover the way the tell-detection layer is.
func plainRepo(t *testing.T) string {
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

// TestCommitMsg_RejectsAVagueOpenWord pins the vocabulary half of #329's
// subject deny list: an open word that says how much changed, not what.
func TestCommitMsg_RejectsAVagueOpenWord(t *testing.T) {
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
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "Rename the helper\n"))
	if !got.Blocked {
		t.Fatal("a three-word subject was allowed")
	}
}

// TestCommitMsg_RejectsAFileListSubject pins the "subject that lists the
// touched files" rule: two or more path-shaped tokens and nothing else.
func TestCommitMsg_RejectsAFileListSubject(t *testing.T) {
	root := plainRepo(t)
	got := CommitMsg(root, msgFile(t, "internal/foo.go and internal/bar.rs\n"))
	if !got.Blocked {
		t.Fatal("a subject naming two files and nothing else was allowed")
	}
}

// TestCommitMsg_AllowsAnOrdinarySubject pins the negative: a normal,
// specific subject must not trip any default check.
func TestCommitMsg_AllowsAnOrdinarySubject(t *testing.T) {
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
	root := plainRepo(t)
	stageLines(t, root, 60)
	got := CommitMsg(root, msgFile(t, "Add the big generated fixture file\n"))
	if !got.Blocked {
		t.Fatal("a 60-line staged diff with no body was allowed")
	}
}

// TestCommitMsg_ALargeDiffWithAnExplanationIsAllowed pins the other side: a
// body that actually explains the change lets the same diff through.
func TestCommitMsg_ALargeDiffWithAnExplanationIsAllowed(t *testing.T) {
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
	root := plainRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\ncommit-message-allow = [\"^v\\d+\\.\\d+\\.\\d+$\"]\n")
	got := CommitMsg(root, msgFile(t, "v1.2.3\n"))
	if got.Blocked {
		t.Fatalf("an allow-listed release-bump subject was rejected: %s", got.Message)
	}
}
