package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// msgFile writes a commit message to a file and returns its path.
func msgFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// undercoverRepo is a repo whose workspace manifest opts into the check.
func undercoverRepo(t *testing.T, on bool) string {
	t.Helper()
	root := t.TempDir()
	manifest := "[workspace]\n"
	if on {
		manifest += "[workspace.metadata.aphrollo]\nundercover = true\n"
	}
	write(t, root, "Cargo.toml", manifest)
	return root
}

// TestCommitMsg_RejectsATellAndNamesTheLine pins the whole point: the message
// is the one artefact that leaves the machine, and a rejection that does not
// SHOW the offending line makes the author guess which of thirty lines tripped
// it.
func TestCommitMsg_RejectsATellAndNamesTheLine(t *testing.T) {
	bad := []struct{ name, body string }{
		{"trailer", "Fix the thing\n\nCo-Authored-By: Someone <s@example.com>\n"},
		{"model name", "Fix the thing\n\nWritten with Claude's help\n"},
		{"vendor", "Fix the thing\n\nAnthropic tooling made this easier\n"},
		{"generated-with", "Fix the thing\n\nGenerated with a code assistant\n"},
		{"model id opus", "Fix the thing\n\nran under opus-5\n"},
		{"model id sonnet", "Fix the thing\n\nsonnet-4 wrote the test\n"},
		{"model id haiku", "Fix the thing\n\nhaiku-3 drafted it\n"},
		{"codename fable", "Fix the thing\n\nper the fable harness\n"},
		{"product", "Fix the thing\n\nclaude-code ran the gate\n"},
		{"internal link", "Fix the thing\n\nsee go/build-policy for the rule\n"},
		// Inline, not leading: a line STARTING with # is a git comment and
		// never reaches history, so this pattern is about a mention inside
		// a real line.
		{"tag", "Fix the thing\n\nasked for #claude-review on this\n"},
		{"org", "Fix the thing\n\nsee anthropics/aphrollo#12\n"},
		{"ai assistant", "Fix the thing\n\nAI assistant paired on this\n"},
		{"ai generated", "Fix the thing\n\nAI generated the fixture\n"},
		{"codename capybara", "Fix the thing\n\nthe Capybara run agreed\n"},
		{"codename tengu", "Fix the thing\n\nTengu flagged it\n"},
		{"merge commit", "Merge branch 'lane/x'\n\nCo-Authored-By: Someone <s@example.com>\n"},
	}
	root := undercoverRepo(t, true)
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			got := CommitMsg(root, msgFile(t, c.body))
			if !got.Blocked {
				t.Fatalf("message allowed:\n%s", c.body)
			}
			offending := strings.Split(strings.TrimSpace(c.body), "\n")
			want := offending[len(offending)-1]
			if !strings.Contains(got.Message, want) {
				t.Fatalf("rejection = %q, want it to quote the offending line %q", got.Message, want)
			}
		})
	}
}

// TestCommitMsg_AllowsAnOrdinaryMessage pins the other half: the check must
// not fire on ordinary prose, or every commit becomes a fight with the hook.
func TestCommitMsg_AllowsAnOrdinaryMessage(t *testing.T) {
	good := []struct{ name, body string }{
		{"plain", "Refuse a commit whose suite never finished\n\nThe untested code stays in history either way.\n"},
		{"comments are ignored", "Fix the thing\n\n# Co-Authored-By: Someone <s@example.com>\n# Please enter the commit message\n"},
		{"ai as a word part", "Fix the retail pipeline\n\nThe AIR filter case is covered.\n"},
		{"bare ai without a following word", "Fix the thing\n\nThe AI is not mentioned as an author here.\n"},
		{"claude as a substring of nothing", "Rename claudication_test to intermittent_test\n"},
	}
	root := undercoverRepo(t, true)
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			if got := CommitMsg(root, msgFile(t, c.body)); got.Blocked {
				t.Fatalf("ordinary message rejected (%s):\n%s", got.Message, c.body)
			}
		})
	}
}

// TestCommitMsg_OffUnlessTheWorkspaceAsksForIt pins the opt-in: a repo that
// has not set undercover = true never sees this hook's opinion, so installing
// the gate everywhere cannot start rejecting anyone's commits.
func TestCommitMsg_OffUnlessTheWorkspaceAsksForIt(t *testing.T) {
	root := undercoverRepo(t, false)
	body := "Fix the thing\n\nCo-Authored-By: Someone <s@example.com>\n"
	if got := CommitMsg(root, msgFile(t, body)); got.Blocked {
		t.Fatalf("a repo that never opted in was blocked: %s", got.Message)
	}
	if got := CommitMsg(t.TempDir(), msgFile(t, body)); got.Blocked {
		t.Fatal("a repo with no manifest at all must pass through")
	}
}

// TestCommitMsg_HonoursThePerRepoDenyList pins the extension seam: the
// built-in list cannot know a repo's own tells, so the workspace can add its
// own patterns beside the flag.
func TestCommitMsg_HonoursThePerRepoDenyList(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\ncommit-message-deny = [\"(?i)\\bskunkworks\\b\", \"WIP:\"]\n")

	if got := CommitMsg(root, msgFile(t, "Fix the thing\n\nper the Skunkworks plan\n")); !got.Blocked {
		t.Fatal("a repo's own deny pattern must reject")
	}
	if got := CommitMsg(root, msgFile(t, "WIP: still shaping this\n")); !got.Blocked {
		t.Fatal("the second pattern in the list must apply too")
	}
	if got := CommitMsg(root, msgFile(t, "Fix the thing properly\n")); got.Blocked {
		t.Fatalf("an unrelated message was rejected: %s", got.Message)
	}
}

// TestCommitMsg_UnreadableMessageFilePassesThrough pins the failure
// direction: this gate protects a convention, not correctness, so a missing
// or unreadable file must never wedge a commit.
func TestCommitMsg_UnreadableMessageFilePassesThrough(t *testing.T) {
	root := undercoverRepo(t, true)
	if got := CommitMsg(root, filepath.Join(t.TempDir(), "nope")); got.Blocked {
		t.Fatal("an unreadable message file must pass through")
	}
}
