package cli

import (
	"bytes"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A mutation runner's canary reports a leak through the recorder the binary
// installs: the report lands in the escape log, so the count of escapes says a
// test process reached a real repository (#1043).
func TestGitWorldRecorder_ALeakReportedByARunnerIsAnEscapeInTheLog(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := commitRepo(t)

	tdd.NoteGitWorldChange(repo, "test-map build", "the repository's config changed", &bytes.Buffer{})

	var listed bytes.Buffer
	tdd.ListEscapes(&listed, true)
	if !bytes.Contains(listed.Bytes(), []byte("test-map build")) {
		t.Errorf("the escape log does not name the runner:\n%s", listed.String())
	}
}
