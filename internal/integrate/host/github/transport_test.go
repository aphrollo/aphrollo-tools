package github

import (
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/shfake"
)

// gh's stdout and stderr used to be folded together, so a warning line (an
// update notice, a deprecation line, a proxy or auth note) landed inside the
// JSON the verbs parse as data (#883). ExecRunner returns stdout alone.
func TestExecRunner_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	dir := t.TempDir()
	shfake.Install(t, dir, "gh", "#!/bin/sh\nprintf '%s\n' '{\"number\":7}'\nprintf '%s\n' 'warning: a new release of gh is available' 1>&2\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := ExecRunner(t.TempDir(), 0, "pr", "view")
	if err != nil {
		t.Fatalf("ExecRunner: %v", err)
	}
	if want := `{"number":7}` + "\n"; string(got) != want {
		t.Fatalf("ExecRunner = %q, want %q (stdout only)", got, want)
	}
	if strings.Contains(string(got), "release") {
		t.Fatalf("stderr leaked into the data return: %q", got)
	}
}
