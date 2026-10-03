package gitx

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A failed git answers its own stderr as the text and an *exec.ExitError
// carrying the same bytes, so a caller can say why without running it again.
func TestGit_AFailureAnswersGitsOwnStderrAndItsExitError(t *testing.T) {
	text, err := git(t.TempDir(), "rev-parse", "HEAD")

	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want git's *exec.ExitError", err)
	}
	if !strings.Contains(text, "not a git repository") {
		t.Errorf("text = %q, want git's own words about the directory", text)
	}
	if string(exit.Stderr) != text {
		t.Errorf("ExitError.Stderr = %q, want the same bytes as the text %q", exit.Stderr, text)
	}
}

func TestGitOut_AFailureAnswersNothing(t *testing.T) {
	if got := gitOut(t.TempDir(), "rev-parse", "HEAD"); got != "" {
		t.Fatalf("gitOut outside a repository = %q, want empty", got)
	}
}
