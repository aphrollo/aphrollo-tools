package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_NoArgs_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errb.String()), "usage") {
		t.Fatalf("stderr missing usage:\n%s", errb.String())
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "frobnicate") {
		t.Fatalf("stderr should name the unknown command:\n%s", errb.String())
	}
}

func TestRun_RenameSymbol_MissingNewName(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "rename-symbol", "--file", "a.go", "--line", "3", "--symbol", "X"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
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
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--col") && !strings.Contains(errb.String(), "--symbol") {
		t.Fatalf("stderr should mention needing --col or --symbol:\n%s", errb.String())
	}
}

func TestRun_FindReferences_MissingLocator(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "find-references", "--file", "a.go", "--line", "3"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--col") && !strings.Contains(errb.String(), "--symbol") {
		t.Fatalf("stderr should mention needing --col or --symbol:\n%s", errb.String())
	}
}

func TestRun_Guardrail_BlocksLongSleep(t *testing.T) {
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"sleep 600"}}`)
	code := Run([]string{"guardrail", "pretooluse"}, stdin, &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (blocked)", code)
	}
	if !strings.Contains(out.String(), `"decision":"block"`) {
		t.Fatalf("stdout should carry the block decision:\n%s", out.String())
	}
}

func TestRun_Guardrail_AllowsNormalCommand(t *testing.T) {
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	code := Run([]string{"guardrail", "pretooluse"}, stdin, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (allowed)", code)
	}
	if out.Len() != 0 {
		t.Fatalf("allow should be silent, got: %s", out.String())
	}
}

func TestRun_Workspace_NoSub_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errb.String()), "usage") {
		t.Fatalf("stderr missing usage:\n%s", errb.String())
	}
}

func TestRun_Workspace_Prepare_MissingArgs(t *testing.T) {
	var out, errb bytes.Buffer
	// only the repo arg, missing <branch>
	if code := Run([]string{"workspace", "prepare", "/some/repo"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "prepare <repo> <branch>") {
		t.Fatalf("stderr should show prepare usage:\n%s", errb.String())
	}
}

func TestRun_Workspace_UnknownSub(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "frob"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "frob") {
		t.Fatalf("stderr should name the unknown subcommand:\n%s", errb.String())
	}
}

func TestRun_Outline_MissingFile(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"outline"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("outline must be a recognized command, got:\n%s", errb.String())
	}
	if !strings.Contains(strings.ToLower(errb.String()), "file") {
		t.Fatalf("stderr should mention the missing file argument:\n%s", errb.String())
	}
}

func TestRun_Show_MissingSymbol(t *testing.T) {
	var out, errb bytes.Buffer
	// file given but no symbol
	if code := Run([]string{"show", "a.go"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("show must be a recognized command, got:\n%s", errb.String())
	}
	if !strings.Contains(strings.ToLower(errb.String()), "symbol") {
		t.Fatalf("stderr should mention the missing symbol argument:\n%s", errb.String())
	}
}

func TestRun_Help_ListsOutlineAndShow(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "outline") || !strings.Contains(out.String(), "show") {
		t.Fatalf("root usage should list outline and show:\n%s", out.String())
	}
}

func TestRun_Help_ExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
