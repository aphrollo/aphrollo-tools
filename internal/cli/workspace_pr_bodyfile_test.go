package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A body file that cannot be read used to leave the PR with an empty body. The
// verb refuses instead, naming the path, before it resolves anything or opens a PR.
func TestWorkspacePR_AMissingBodyFileRefusesAndNamesThePath(t *testing.T) {
	repo := gitInit(t, map[string]string{"a.txt": "a\n"})
	inDir(t, repo)
	missing := filepath.Join(t.TempDir(), "no-such-body.md")

	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "pr", "--body-file", missing, "--title", "t"}, strings.NewReader(""), &out, &errb)

	if code == 0 {
		t.Fatalf("exit = 0, want non-zero\nstdout: %s", out.String())
	}
	if !strings.Contains(errb.String(), missing) {
		t.Errorf("stderr must name the missing path %q:\n%s", missing, errb.String())
	}
	if strings.Contains(out.String(), "workspace pr:") {
		t.Errorf("a PR plan was printed despite the unreadable body file:\n%s", out.String())
	}
}
