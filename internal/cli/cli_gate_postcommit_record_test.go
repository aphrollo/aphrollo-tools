package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/commitrecord"
)

// The post-commit hook is the one place that vouches a commit came through the
// real commit path: the canary treats a commit with no record as a leak.
func TestRun_Gate_Postcommit_RecordsTheCommitJustMade(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postcommit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate postcommit exit = %d\nstderr: %s", code, errb.String())
	}

	head := strings.TrimSpace(gitOutLine(t, repo, "rev-parse", "HEAD"))
	if !commitrecord.Recorded(repo)[head] {
		t.Fatalf("HEAD %s has no commit record after the post-commit hook ran", head)
	}
}

// A merge commit fires post-merge, never post-commit, so the merge hook vouches
// for it too: the lane owner merging origin/main into a lane is not a leak.
func TestRun_Gate_Postmerge_RecordsTheMergeJustMade(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postmerge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("gate postmerge exit = %d\nstderr: %s", code, errb.String())
	}

	head := strings.TrimSpace(gitOutLine(t, repo, "rev-parse", "HEAD"))
	if !commitrecord.Recorded(repo)[head] {
		t.Fatalf("HEAD %s has no commit record after the post-merge hook ran", head)
	}
}

// A rebase or an amend rewrites commits and fires post-rewrite, with one
// "<old> <new> [extra]" line per commit on stdin; the hook records each new sha.
func TestRun_Gate_Postrewrite_RecordsEachNewSha(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)
	head := strings.TrimSpace(gitOutLine(t, repo, "rev-parse", "HEAD"))
	oldSha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	stdin := oldSha + " " + head + "\n" + oldSha + " " + other + " extra\n" + "garbage line\n"

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postrewrite", "rebase"}, strings.NewReader(stdin), &out, &errb); code != 0 {
		t.Fatalf("gate postrewrite exit = %d\nstderr: %s", code, errb.String())
	}

	got := commitrecord.Recorded(repo)
	if !got[head] || !got[other] {
		t.Errorf("recorded = %v, want both new shas", got)
	}
	if got[oldSha] || got["garbage"] {
		t.Errorf("recorded = %v, want only the new shas", got)
	}
}
