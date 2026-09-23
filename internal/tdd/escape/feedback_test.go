package escape

import (
	"strings"
	"testing"
)

// A session using the gate finds gate bugs, and `gate issue` files them
// against the repo the session is standing in — so a borld session that hits
// an aphrollo defect opens a borld issue, against a repo that cannot fix it.
// Nine defects reached the tool's own tracker in one day only because a human
// or another session relayed each one by hand. Feedback needs a route that
// does not depend on somebody noticing.
func TestUpstreamRepo_DefaultsToTheToolsOwnTracker(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.24\n")

	if got := UpstreamRepo(root); got != DefaultUpstreamRepo {
		t.Errorf("UpstreamRepo = %q, want the tool's own tracker %q — a repo that declares none still has somewhere to send a tool bug", got, DefaultUpstreamRepo)
	}
}

// ...and a repo that vendors or forks the tool sends its feedback wherever it
// says, since the default is only right for repos using the tool as shipped.
func TestUpstreamRepo_HonoursTheDeclaredTracker(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nupstream = \"someone/their-fork\"\n")

	if got := UpstreamRepo(root); got != "someone/their-fork" {
		t.Errorf("UpstreamRepo = %q, want the declared tracker", got)
	}
}

// The same works from a Cargo workspace, which is where a Rust consumer keeps
// its aphrollo settings.
func TestUpstreamRepo_HonoursTheDeclaredTrackerFromCargoMetadata(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nupstream = \"someone/rust-fork\"\n")

	if got := UpstreamRepo(root); got != "someone/rust-fork" {
		t.Errorf("UpstreamRepo = %q, want the declared tracker", got)
	}
}

// gh opens an issue against the repo it is run in unless told otherwise, so
// routing upstream means naming the target explicitly. Without this the
// issue lands in the reporting repo no matter what the caller intended.
func TestIssueArgv_TargetRepoRoutesTheIssueUpstream(t *testing.T) {
	argv := issueArgv(IssueOptions{Title: "t", Body: "b", TargetRepo: "aphrollo/aphrollo-tools"})

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--repo aphrollo/aphrollo-tools") {
		t.Errorf("argv = %q, want it to name the upstream repo — gh would otherwise file into the reporting repo", joined)
	}
}

// ...and an ordinary issue still goes to the repo it was run in, so `gate
// issue` is unchanged.
func TestIssueArgv_WithoutATargetNamesNoRepo(t *testing.T) {
	argv := issueArgv(IssueOptions{Title: "t", Body: "b"})

	if joined := strings.Join(argv, " "); strings.Contains(joined, "--repo") {
		t.Errorf("argv = %q, want no --repo — an ordinary issue belongs to the repo it was filed from", joined)
	}
}

// An upstream issue is useless without saying where it came from: the tool's
// maintainer cannot reproduce a report that does not name the repo, the
// commit, or the tool build that produced it. Every one of today's hand-
// relayed reports carried that context because a human wrote it in.
func TestFeedbackBody_CarriesTheReportingRepoAndTip(t *testing.T) {
	repo := makeGoRepo(t)

	body := FeedbackBody(repo, "the gate refused a pure rename")

	if !strings.Contains(body, "the gate refused a pure rename") {
		t.Error("the reporter's own words must survive")
	}
	tip := gitValue(t, repo, "rev-parse", "HEAD")
	if !strings.Contains(body, tip[:12]) {
		t.Errorf("body = %q, want the reporting tip %s so the report is reproducible", body, tip[:12])
	}
}
