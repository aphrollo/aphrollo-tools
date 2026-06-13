package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_NoArgs_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errb.String()), "usage") {
		t.Fatalf("stderr missing usage:\n%s", errb.String())
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "frobnicate") {
		t.Fatalf("stderr should name the unknown command:\n%s", errb.String())
	}
}

func TestRun_RenameSymbol_MissingNewName(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "rename-symbol", "--file", "a.go", "--line", "3", "--symbol", "X"}
	if code := Run(args, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "new-name") {
		t.Fatalf("stderr should mention missing --new-name:\n%s", errb.String())
	}
}

func TestRun_RenameSymbol_MissingLocator(t *testing.T) {
	var out, errb bytes.Buffer
	// neither --col nor --symbol given
	args := []string{"refactor", "rename-symbol", "--file", "a.go", "--line", "3", "--new-name", "Y"}
	if code := Run(args, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--col") && !strings.Contains(errb.String(), "--symbol") {
		t.Fatalf("stderr should mention needing --col or --symbol:\n%s", errb.String())
	}
}

func TestRun_Help_ExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
