package cli

import (
	"bytes"
	"strings"
	"testing"
)

// refactor executes by default, so --apply is a legacy no-op that still says so.
func TestRefactor_ApplyIsALegacyNoOpWithANotice(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "--file", "a.go", "--line", "3", "--symbol", "X", "--apply"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (missing --new-name)", code)
	}
	if !strings.Contains(errb.String(), "aphrollo refactor: --apply is a no-op") {
		t.Fatalf("stderr lacks the --apply notice:\n%s", errb.String())
	}
}

func TestRefactor_DryIsAccepted(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "--file", "a.go", "--line", "3", "--symbol", "X", "--dry"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2 (missing --new-name)", code)
	}
	if strings.Contains(errb.String(), "flag provided but not defined") {
		t.Fatalf("refactor rejected --dry:\n%s", errb.String())
	}
	if strings.Contains(errb.String(), "no-op") {
		t.Fatalf("--dry printed the --apply notice:\n%s", errb.String())
	}
}

func TestRefactor_StrayArgumentIsRefusedNotSwallowed(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "stray", "--new-name", "Y"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unexpected argument "stray"`) {
		t.Fatalf("stderr should name the stray argument:\n%s", errb.String())
	}
}
