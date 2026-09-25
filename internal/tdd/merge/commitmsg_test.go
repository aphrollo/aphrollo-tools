package merge

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func msgFile(t *testing.T, body string) string { t.Helper(); return tddtest.MsgFile(t, body) }

func undercoverRepo(t *testing.T, on bool) string { t.Helper(); return tddtest.UndercoverRepo(t, on) }

// TestCommitMsg_RejectsATellAndNamesTheLine pins the whole point: the message
// is the one artefact that leaves the machine, and a rejection that does not
// SHOW the offending line makes the author guess which of thirty lines tripped
// it.
func TestCommitMsg_RejectsATellAndNamesTheLine(t *testing.T) {
	t.Parallel()
	bad := []struct{ name, body string }{
		{"trailer", "Fix the flaky retry timer\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n"},
		{"model name", "Fix the flaky retry timer\n\nWritten with Claude's help\n"},
		{"vendor", "Fix the flaky retry timer\n\nAnthropic tooling made this easier\n"},
		{"generated-with", "Fix the flaky retry timer\n\nGenerated with a code assistant\n"},
		{"model id opus", "Fix the flaky retry timer\n\nran under opus-5\n"},
		{"model id sonnet", "Fix the flaky retry timer\n\nsonnet-4 wrote the test\n"},
		{"model id haiku", "Fix the flaky retry timer\n\nhaiku-3 drafted it\n"},
		{"product", "Fix the flaky retry timer\n\nclaude-code ran the gate\n"},
		// Inline, not leading: a line STARTING with # is a git comment and
		// never reaches history, so this pattern is about a mention inside
		// a real line.
		{"tag", "Fix the flaky retry timer\n\nasked for #claude-review on this\n"},
		{"org", "Fix the flaky retry timer\n\nsee anthropics/aphrollo#12\n"},
		{"session link", "Fix the flaky retry timer\n\nhttps://claude.ai/code/session_01\n"},
		{"merge commit", "Merge branch 'lane/x'\n\nCo-authored-by: Copilot <175728472+Copilot@users.noreply.github.com>\n"},
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
	t.Parallel()
	good := []struct{ name, body string }{
		{"plain", "Refuse a commit whose suite never finished\n\nThe untested code stays in history either way.\n"},
		{"comments are ignored", "Fix the flaky retry timer\n\n# Co-Authored-By: Someone <s@example.com>\n# Please enter the commit message\n"},
		{"ai as a word part", "Fix the retail pipeline\n\nThe AIR filter case is covered.\n"},
		{"bare ai without a following word", "Fix the flaky retry timer\n\nThe AI is not mentioned as an author here.\n"},
		{"claude as a substring of nothing", "Rename claudication_test to intermittent_test\n"},
		// Each of these was refused by the list this one replaced, and each
		// is ordinary prose.
		{"a human co-author", "Fix the flaky retry timer\n\nCo-Authored-By: Jane Doe <jane@example.com>\n"},
		{"a fable", "Add the fable about the tortoise to the reader fixtures\n"},
		{"a go path", "Vendor tools/go/analysis for the linter\n"},
		{"a product feature", "Add the AI assistant panel to the dashboard\n"},
		{"a ruby test library", "Port the Capybara feature specs to the new router\n"},
		{"a generator", "Generated with protoc from api.proto\n"},
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
	t.Parallel()
	root := undercoverRepo(t, false)
	body := "Fix the flaky retry timer\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"
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
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\ncommit-message-deny = [\"(?i)\\bskunkworks\\b\", \"WIP:\"]\n")

	if got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n\nper the Skunkworks plan\n")); !got.Blocked {
		t.Fatal("a repo's own deny pattern must reject")
	}
	if got := CommitMsg(root, msgFile(t, "WIP: still shaping this\n")); !got.Blocked {
		t.Fatal("the second pattern in the list must apply too")
	}
	if got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer properly\n")); got.Blocked {
		t.Fatalf("an unrelated message was rejected: %s", got.Message)
	}
}

// TestCommitMsg_UnreadableMessageFilePassesThrough pins the failure
// direction: this gate protects a convention, not correctness, so a missing
// or unreadable file must never wedge a commit.
func TestCommitMsg_UnreadableMessageFilePassesThrough(t *testing.T) {
	t.Parallel()
	root := undercoverRepo(t, true)
	if got := CommitMsg(root, filepath.Join(t.TempDir(), "nope")); got.Blocked {
		t.Fatal("an unreadable message file must pass through")
	}
}

// A repo whose own guidance file is called CLAUDE.md cannot describe editing
// it: the file NAME is not a tell about how the commit was written.
func TestCommitMsg_TheGuidanceFileNameIsNotATell(t *testing.T) {
	t.Parallel()
	root := undercoverRepo(t, true)

	allowed := []string{
		"Add the aphrollo-managed operating block to CLAUDE.md",
		"Document the gate stages in claude.md and the README",
	}
	for _, body := range allowed {
		if res := CommitMsg(root, msgFile(t, body+"\n")); res.Blocked {
			t.Errorf("naming the file must pass: %q\n%s", body, res.Message)
		}
	}

	if res := CommitMsg(root, msgFile(t, "Claude wrote this\n")); !res.Blocked {
		t.Error("the tell itself must still be rejected")
	}
	if res := CommitMsg(root, msgFile(t, "Ask Claude.md-style questions of Claude next time\n")); !res.Blocked {
		t.Error("a tell elsewhere on a line that also names the file must still be rejected")
	}
}

func TestCommitMsg_RefsAndPathsSpelledWithTheWordAreNotTells(t *testing.T) {
	t.Parallel()
	root := undercoverRepo(t, true)

	allowed := []string{
		"Merge lane/claude-md: project guide as tagged laws",
		"Move the code-quality skill under .claude/skills",
		"Regenerated with cargo hakari after the rename",
	}
	for _, body := range allowed {
		if res := CommitMsg(root, msgFile(t, body+"\n")); res.Blocked {
			t.Errorf("a ref, a path or an ordinary word must pass: %q\n%s", body, res.Message)
		}
	}

	if res := CommitMsg(root, msgFile(t, "Generated with Claude Code\n")); !res.Blocked {
		t.Error("the whole-word tell must still be rejected")
	}
	if res := CommitMsg(root, msgFile(t, "Merge lane/claude-md as Claude suggested\n")); !res.Blocked {
		t.Error("a tell beside a scrubbed ref must still be rejected")
	}
}

// A repo with no Cargo.toml has no `[workspace.metadata.aphrollo]` to set the
// flag in, so for a Go, Python or Node repo the undercover check reads a root
// `aphrollo.toml` instead — the same fallback the mutation job already uses.
//
// Without it the gate is not merely unset but UNSETTABLE there: every message
// passes, and this repo's own history proves the cost — 104 commits carrying a
// `Co-Authored-By:` trailer reached a public remote through a hook that was
// installed, ran, and returned clean on every one of them.
func TestCommitMsg_ReadsTheUndercoverFlagFromAphrolloTomlWhenThereIsNoCargoToml(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.24\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")

	got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"))
	if !got.Blocked {
		t.Error("a message carrying a Co-Authored-By trailer was accepted in a Go repo that asked to stay undercover")
	}
}

// ...and the flag still has to be ASKED for: a repo that never opted in must
// not start having its commits rejected the day the hook is installed.
func TestCommitMsg_LeavesAGoRepoThatNeverAskedAlone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.24\n")

	got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"))
	if got.Blocked {
		t.Errorf("a repo that never set undercover had a commit rejected: %s", got.Message)
	}
}

// identityRepo is an undercover repo that is also a git repo, its identity
// set in its own config so the test never reads the box's.
func identityRepo(t *testing.T, on bool, name, email string) string {
	t.Helper()
	root := undercoverRepo(t, on)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", name},
		{"config", "user.email", email},
	} {
		if out, err := git(root, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// The session that pushed a tell branch also started with git's identity set
// to the tool's own, and nothing would have refused a commit authored that
// way: the message was clean, the name on it was not.
func TestCommitMsg_RefusesAToolIdentityAndNamesTheFix(t *testing.T) {
	t.Parallel()
	root := identityRepo(t, true, "Claude", "noreply@anthropic.com")

	got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n"))
	if !got.Blocked {
		t.Fatal("a commit authored as the tool was accepted")
	}
	for _, want := range []string{"Claude <noreply@anthropic.com>", "git config user.name", "git config user.email"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("refusal %q must name %q", got.Message, want)
		}
	}
}

func TestCommitMsg_RefusesAToolCommitterUnderAPersonsAuthorship(t *testing.T) {
	root := identityRepo(t, true, "Jane Doe", "jane@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Claude")

	got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n"))
	if !got.Blocked || !strings.Contains(got.Message, "committer") {
		t.Fatalf("a commit committed as the tool must be refused naming the committer, got %+v", got)
	}
}

func TestCommitMsg_AcceptsAPersonsIdentity(t *testing.T) {
	t.Parallel()
	root := identityRepo(t, true, "Claudia Airey", "claudia@example.com")
	if got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n")); got.Blocked {
		t.Fatalf("an ordinary identity was refused: %s", got.Message)
	}
}

func TestCommitMsg_IgnoresTheIdentityWhenUndercoverIsOff(t *testing.T) {
	t.Parallel()
	root := identityRepo(t, false, "Claude", "noreply@anthropic.com")
	if got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n")); got.Blocked {
		t.Fatalf("a repo that never opted in was refused on its identity: %s", got.Message)
	}
}

// The refusal names the line by its number too, counted from 1, so an author
// with a long message goes straight to it.
func TestCommitMsg_RejectionNumbersTheOffendingLine(t *testing.T) {
	t.Parallel()
	root := undercoverRepo(t, true)
	got := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n\nran under opus-5\n"))
	if !got.Blocked || !strings.Contains(got.Message, "line 3 ") {
		t.Fatalf("rejection = %q, want it to name line 3", got.Message)
	}
}
