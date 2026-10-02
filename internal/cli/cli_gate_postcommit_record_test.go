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
