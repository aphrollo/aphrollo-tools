package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An open point parked in a markdown file is a note nobody triages. The
// issue writer is what turns it into a row somebody can filter — so the URL
// it prints has to be the real one gh reported, not a guess.
func TestOpenIssuePrintsTheURLGhReported(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "https://github.com/o/r/issues/77")

	url, number, err := OpenIssue(IssueOptions{
		Repo:   repo,
		Title:  "the tire rig drifts at 60 Hz",
		Body:   "seen on the rolling-drag bench",
		Labels: []string{"physics"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/o/r/issues/77" || number != 77 {
		t.Fatalf("OpenIssue = (%q, %d), want the URL gh printed and its number", url, number)
	}
}

// `gh issue create --label` FAILS outright on a label that does not exist, so
// the label is created first. Creating it once per process is the whole point
// of the cache: a sync of twenty issues must not make twenty identical calls.
func TestOpenIssueCreatesEachLabelOnlyOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/1")
	resetLabelCache()

	for range 3 {
		if _, _, err := OpenIssue(IssueOptions{
			Repo: repo, Title: "t", Body: "b", Labels: []string{"physics"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(ghArgv(t, log), "label create physics"); n != 1 {
		t.Fatalf("gh ran `label create physics` %d times, want 1:\n%s", n, ghArgv(t, log))
	}
}

// A repo that declares its themes has exactly those themes. A typo'd label
// silently opens a NEW theme nobody filters on, so it is refused — and the
// refusal carries the list, because "unknown label" without it makes the
// author guess.
func TestOpenIssueRefusesALabelOutsideTheDeclaredList(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	writeIssueLabels(t, repo, "netcode", "physics")
	stubGh(t, "https://github.com/o/r/issues/1")

	_, _, err := OpenIssue(IssueOptions{Repo: repo, Title: "t", Labels: []string{"phsyics"}})
	if err == nil {
		t.Fatal("a label outside the declared list must be refused")
	}
	for _, want := range []string{"phsyics", "netcode", "physics"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q; got %q", want, err)
		}
	}
}

// The list is a guard against typos, not a freeze: a deliberate new theme is
// declared on the command line and goes through.
func TestOpenIssueAcceptsANewLabelWhenAsked(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	writeIssueLabels(t, repo, "netcode")
	stubGh(t, "https://github.com/o/r/issues/9")
	resetLabelCache()

	if _, _, err := OpenIssue(IssueOptions{
		Repo: repo, Title: "t", Labels: []string{"audio"}, AllowNewLabel: true,
	}); err != nil {
		t.Fatalf("--new-label must admit an undeclared theme: %v", err)
	}
}

// A repo that declares no list has nothing to check against, and refusing
// every label there would make the command unusable in a fresh repo.
func TestOpenIssueAllowsAnyLabelWhenTheRepoDeclaresNone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGh(t, "https://github.com/o/r/issues/3")
	resetLabelCache()

	if _, _, err := OpenIssue(IssueOptions{Repo: repo, Title: "t", Labels: []string{"anything"}}); err != nil {
		t.Fatalf("with no declared list every label is allowed: %v", err)
	}
}

// A non-cargo repo configures the gate through aphrollo.toml, and the list
// must be read from there too — otherwise every Go or Zig repo silently has
// no declared themes.
func TestIssueLabelsReadsAphrolloTomlForANonCargoRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"),
		[]byte("[aphrollo]\nissue-labels = [\"tooling\", \"docs\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := IssueLabels(repo)
	if len(got) != 2 || got[0] != "docs" || got[1] != "tooling" {
		t.Fatalf("IssueLabels = %v, want the sorted aphrollo.toml list", got)
	}
}

// The managed CLAUDE.md block is the only place a session learns the loop
// exists. If it does not name both verbs, every open point goes back to
// being a markdown follow-up.
func TestClaudeMDBlockNamesTheIssueAndEscapeVerbs(t *testing.T) {
	block := ClaudeMDBlock("/tmp/shims", false)
	for _, want := range []string{
		"aphrollo issue",
		"aphrollo gate escape record",
		"never a markdown follow-up",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the managed block must state %q:\n%s", want, block)
		}
	}
}

// writeIssueLabels declares repo's themes in its cargo manifest.
func writeIssueLabels(t *testing.T, repo string, labels ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\nissue-labels = [")
	for i, l := range labels {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`"` + l + `"`)
	}
	b.WriteString("]\n")
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `gh label create` failing (no auth, no network, a name gh will not take)
// must not be remembered as done: the next issue in the same process would
// then ask for a label that does not exist, and gh refuses the whole create.
func TestAFailedLabelCreateIsRetriedNotCached(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/1")
	resetLabelCache()
	t.Setenv("GH_STUB_LABEL_CREATE_FAIL", "gh: could not resolve host")

	if _, _, err := OpenIssue(IssueOptions{Repo: repo, Title: "t", Labels: []string{"physics"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenIssue(IssueOptions{Repo: repo, Title: "t", Labels: []string{"physics"}}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(ghArgv(t, log), "label create physics"); n != 2 {
		t.Fatalf("a failed label create must be retried: ran %d times, want 2:\n%s", n, ghArgv(t, log))
	}
}
