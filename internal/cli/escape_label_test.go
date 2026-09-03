package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A playtest defect belongs in the project's own theme filter, beside every
// other physics issue — not in a pile only the gate reads.
func TestEscapeRecordCarriesTheThemeLabelOntoTheIssue(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/5")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "escape", "record", "the tire sinks through the terrain at 60 Hz",
		"--label", "physics", "--check", "forge_solver ground-contact settling test", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--label escape", "--label physics", "ground-contact settling test"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("gh argv must carry %q:\n%s", want, argv)
		}
	}
}

// An escape is a claim ABOUT THE GATE: some check could have caught this and
// did not. A themed defect with no check named is not that claim — it is a
// plain defect, and plain defects go through `gate issue`.
func TestEscapeRecordWithAThemeRefusesWithoutTheCheckThatCouldHaveCaughtIt(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/5")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "escape", "record", "the tire sinks through the terrain", "--label", "physics", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatal("a themed defect with no check named must be refused")
	}
	for _, want := range []string{"--check", "gate issue"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the refusal must point at %q; stderr was %q", want, errb.String())
		}
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "issue create") {
		t.Errorf("a refused record must never reach gh:\n%s", data)
	}
}

// A false positive is about a check refusing correct work, so it needs no
// check to have caught it — the theme is just where the work lives.
func TestAThemedFalsePositiveNeedsNoCheck(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/6")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "escape", "record", "the law refused a correct name",
		"--kind", "false-positive", "--label", "quality", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--label false-positive", "--label quality"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("gh argv must carry %q:\n%s", want, argv)
		}
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "gate-state", "escapes.jsonl"))
	if err != nil {
		t.Fatalf("nothing recorded: %v", err)
	}
	for _, want := range []string{`"kind":"false-positive"`, `"quality"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record must carry %s:\n%s", want, data)
		}
	}
}

// CI red on a tip the local gate never passed is not evidence about the gate.
// Recording it would fill the loop with pushes nobody gated.
func TestEscapeRecordFromCIRecordsNothingWhenTheTipCarriesNoGreenGate(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/7")
	commitSomething(t, repo, "Land it")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "escape", "record", "clippy failed", "--from-ci", "build", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the recorder never fails a CI job\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String()+errb.String(), "no green gate") {
		t.Errorf("the recorder must say why it recorded nothing: %q / %q", out.String(), errb.String())
	}
	if data, err := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "gate-state", "escapes.jsonl")); err == nil && len(data) > 0 {
		t.Errorf("nothing must be recorded:\n%s", data)
	}
}

// commitSomething makes one ordinary commit, with no gate trailer.
func commitSomething(t *testing.T, repo, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", subject}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

// The theme and the check are the whole point of recording a playtest or CI
// defect on the project repo. Dropping them on the --from-ci path files the
// issue where nobody filtering by theme will ever see it.
func TestEscapeRecordFromCICarriesTheThemeAndCheckThrough(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/8")
	commitWithGateNote(t, repo, "Land it")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "escape", "record", "the suite went red in CI",
		"--from-ci", "build", "--label", "quality", "--check", "precommit mechanical stage", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--label quality", "precommit mechanical stage"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("the --from-ci path must carry %q through:\n%s", want, argv)
		}
	}
}

// commitWithGateNote makes a commit carrying the gate note for its own tree,
// which is what the --from-ci path requires before it records anything.
func commitWithGateNote(t *testing.T, repo, subject string) {
	t.Helper()
	commitSomething(t, repo, subject)
	tree := gitLine(t, repo, "rev-parse", "HEAD:")
	gitRun(t, repo, "notes", "--ref=gate", "add", "-f", "-m", "green "+tree, "HEAD")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func gitLine(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
