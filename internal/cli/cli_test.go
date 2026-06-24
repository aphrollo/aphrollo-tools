package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGit points git's global/system config at temp files so a test that
// runs `tdd init` never mutates the runner's real ~/.gitconfig.
func isolateGit(t *testing.T) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

// `tdd init --no-git` writes the session hooks into the given config dir and is
// reversible with --uninstall.
func TestRun_TDDInit(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", dir, "--bin", "/usr/local/bin/aphrollo", "--no-git"},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), "/usr/local/bin/aphrollo tdd pretooluse") {
		t.Errorf("settings.json missing wired hook:\n%s", data)
	}

	out.Reset()
	errb.Reset()
	code = Run([]string{"tdd", "init", "--config-dir", dir, "--uninstall", "--no-git"},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("uninstall exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	data, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(data), "aphrollo tdd") {
		t.Errorf("uninstall left hooks behind:\n%s", data)
	}
}

// `tdd init` (no --no-git) also installs the git gate: shims plus a global
// core.hooksPath. One command sets up everything.
func TestRun_TDDInit_GitGate(t *testing.T) {
	isolateGit(t)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--bin", "/usr/local/bin/aphrollo"},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err != nil {
		t.Errorf("pre-commit shim not installed: %v", err)
	}
	hp, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if strings.TrimSpace(string(hp)) != hooks {
		t.Errorf("core.hooksPath = %q, want %q", strings.TrimSpace(string(hp)), hooks)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--uninstall"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); !os.IsNotExist(err) {
		t.Error("pre-commit shim survived uninstall")
	}
	hp, _ = exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	if strings.TrimSpace(string(hp)) != "" {
		t.Errorf("core.hooksPath still set after uninstall: %q", strings.TrimSpace(string(hp)))
	}
}

// `tdd prepush` is a mechanical-only no-op: it exits 0 and never blocks, so a
// pre-push shim lingering on a box installed before the LLM review was removed
// can never wedge a push. It must NOT depend on being inside a git repo or on
// any external reviewer — it returns immediately.
func TestRun_TDDPrepush_IsNoOp(t *testing.T) {
	// Run from a non-repo temp dir to prove prepush does no git/repo work.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "prepush"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("prepush exit = %d, want 0 (must never block)\nstderr: %s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("prepush should emit nothing on stdout, got:\n%s", out.String())
	}
}

func TestRun_Sqlc_NoSub_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"sqlc"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("bare `sqlc` should be a usage error (2), got %d", code)
	}
	if !strings.Contains(errb.String(), "check") || !strings.Contains(errb.String(), "regen") {
		t.Errorf("usage should list check + regen:\n%s", errb.String())
	}
}

func TestRun_Sqlc_Help(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"sqlc", "--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("`sqlc --help` should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "models.go") {
		t.Errorf("sqlc help should document the whole-schema models.go gotcha:\n%s", out.String())
	}
}

func TestRun_Sqlc_UnknownSub(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"sqlc", "frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("unknown sqlc subcommand should be 2, got %d", code)
	}
}

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

// `aphrollo refactor` IS the rename: flags parse directly on the verb, no
// rename-symbol subcommand.
func TestRun_Refactor_MissingNewName(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "--file", "a.go", "--line", "3", "--symbol", "X"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "new-name") {
		t.Fatalf("stderr should mention missing --new-name:\n%s", errb.String())
	}
}

func TestRun_Refactor_MissingLocator(t *testing.T) {
	var out, errb bytes.Buffer
	// neither --col nor --symbol given
	args := []string{"refactor", "--file", "a.go", "--line", "3", "--new-name", "Y"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--col") && !strings.Contains(errb.String(), "--symbol") {
		t.Fatalf("stderr should mention needing --col or --symbol:\n%s", errb.String())
	}
}

// `refactor` stays dry-run-by-default + --apply (no inversion): missing required
// flags are still usage errors, and the --apply flag is accepted.
func TestRun_Refactor_AcceptsApplyFlag(t *testing.T) {
	var out, errb bytes.Buffer
	// --apply is parsed but the missing --new-name still trips the usage gate.
	args := []string{"refactor", "--file", "a.go", "--line", "3", "--symbol", "X", "--apply"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if strings.Contains(errb.String(), "flag provided but not defined") {
		t.Fatalf("refactor should accept --apply directly:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "new-name") {
		t.Fatalf("stderr should mention missing --new-name:\n%s", errb.String())
	}
}

// The rename-symbol subcommand is GONE: an old caller now falls through to the
// flag parser, which rejects "rename-symbol" as an undefined positional/flag.
func TestRun_Refactor_RenameSymbolSubcommand_Gone(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "rename-symbol", "--file", "a.go", "--line", "3", "--symbol", "X", "--new-name", "Y"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("removed rename-symbol subcommand should be a usage error (2), got %d", code)
	}
}

// `aphrollo find` is top-level (the old refactor find-references): flags parse
// directly on the verb.
func TestRun_Find_MissingLocator(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"find", "--file", "a.go", "--line", "3"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--col") && !strings.Contains(errb.String(), "--symbol") {
		t.Fatalf("stderr should mention needing --col or --symbol:\n%s", errb.String())
	}
}

// `refactor find-references` is GONE: find-references is no longer a refactor
// subcommand, so an old caller is a usage error.
func TestRun_Refactor_FindReferencesSubcommand_Gone(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"refactor", "find-references", "--file", "a.go", "--line", "3", "--symbol", "X"}
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("removed find-references subcommand should be a usage error (2), got %d", code)
	}
}

// find is advertised in the root usage next to outline/show/refactor.
func TestRun_Help_ListsFind(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "find") {
		t.Fatalf("root usage should list find:\n%s", out.String())
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

func TestRun_TDD_BlocksTautologyInTest(t *testing.T) {
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"a_test.go","content":"assert x == x"}}`)
	code := Run([]string{"tdd", "pretooluse"}, stdin, &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (blocked)", code)
	}
	if !strings.Contains(out.String(), `"decision":"block"`) {
		t.Fatalf("stdout should carry the block decision:\n%s", out.String())
	}
}

func TestRun_TDD_AllowsSourceEdit(t *testing.T) {
	// An absolute path under no git repo so the worktree advisory stays silent
	// (it warns once per session in a main clone) — this asserts the CONTENT
	// gate lets a sleep in a source file flow, independent of where tests run.
	file := filepath.Join(t.TempDir(), "a.go")
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Edit","tool_input":{"file_path":"` + file + `","new_string":"time.Sleep(2)"}}`)
	code := Run([]string{"tdd", "pretooluse"}, stdin, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (allowed)", code)
	}
	if out.Len() != 0 {
		t.Fatalf("allow should be silent, got: %s", out.String())
	}
}

func TestRun_TDD_SessionStart_NudgesSkills(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"cli-ss"}`)
	code := Run([]string{"tdd", "sessionstart"}, stdin, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "test-driven-development") ||
		!strings.Contains(out.String(), `"hookEventName":"SessionStart"`) {
		t.Fatalf("sessionstart should inject the skill nudge:\n%s", out.String())
	}
}

func TestRun_TDD_MalformedInputFailsOpen(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "pretooluse"}, strings.NewReader("{bad"), &out, &errb)
	if code != 0 {
		t.Fatalf("malformed input must fail open (exit 0), got %d", code)
	}
}

func TestRun_TDD_UserPromptSubmit_TddCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"prompt":"/tdd off","session_id":"cli-sess"}`)
	code := Run([]string{"tdd", "userpromptsubmit"}, stdin, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), `"decision":"block"`) || !strings.Contains(out.String(), "OFF") {
		t.Fatalf("/tdd off should block-and-report:\n%s", out.String())
	}
}

func TestRun_TDD_SessionEnd_Silent(t *testing.T) {
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"cli-sess"}`)
	code := Run([]string{"tdd", "sessionend"}, stdin, &out, &errb)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("sessionend should be silent exit 0, got code=%d out=%q", code, out.String())
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

func TestRun_Workspace_Create_MissingArgs(t *testing.T) {
	var out, errb bytes.Buffer
	// only the repo arg, missing <branch>
	if code := Run([]string{"workspace", "create", "/some/repo"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "create <repo> <branch>") {
		t.Fatalf("stderr should show create usage:\n%s", errb.String())
	}
}

// The legacy workspace aliases are GONE — no back-compat. An old name now falls
// through to the normal unknown-subcommand error (exit 2, naming the token).
func TestRun_Workspace_LegacyAliases_AreGone(t *testing.T) {
	for _, alias := range []string{"prepare", "ready", "cleanup"} {
		var out, errb bytes.Buffer
		if code := Run([]string{"workspace", alias, "/some/repo"}, strings.NewReader(""), &out, &errb); code != 2 {
			t.Fatalf("removed alias %q should be a usage error (2), got %d", alias, code)
		}
		if !strings.Contains(errb.String(), alias) {
			t.Fatalf("stderr should name the unknown subcommand %q:\n%s", alias, errb.String())
		}
		if !strings.Contains(errb.String(), "unknown subcommand") {
			t.Fatalf("removed alias %q should hit the unknown-subcommand path:\n%s", alias, errb.String())
		}
	}
}

// Help advertises only the clean verbs — no alias is mentioned.
func TestRun_Workspace_Help_NoAliasMentions(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "create <repo> <branch>") {
		t.Fatalf("workspace help should advertise create:\n%s", out.String())
	}
	// Guard the command-surface form (a two-space-indented verb in the verb
	// list), not incidental prose like "prepared worktree" or "ready for review".
	for _, alias := range []string{"prepare", "ready", "cleanup"} {
		if strings.Contains(out.String(), "\n  "+alias+" ") {
			t.Fatalf("workspace help should NOT list the removed alias %q as a verb:\n%s", alias, out.String())
		}
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

func TestRun_Workspace_Commit_MissingMessage(t *testing.T) {
	var out, errb bytes.Buffer
	// In this repo's worktree, cwd-resolution succeeds but the empty -m is rejected.
	code := Run([]string{"workspace", "commit"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (missing message)", code)
	}
	if !strings.Contains(errb.String(), "message") {
		t.Fatalf("stderr should mention the missing commit message:\n%s", errb.String())
	}
}

// commitRepo builds a temp git repo with one staged-but-uncommitted change and
// chdir's into it, so a cwd-only verb resolves it. Returns the repo path.
func commitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "seed")
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	return repo
}

func headSubject(t *testing.T, repo string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", repo, "log", "-1", "--pretty=%s").Output()
	return strings.TrimSpace(string(out))
}

// Slice 2: commit EXECUTES BY DEFAULT (no --apply needed).
func TestRun_Workspace_Commit_AppliesByDefault(t *testing.T) {
	repo := commitRepo(t)
	var out, errb bytes.Buffer
	// no-verify so the global TDD gate doesn't run inside the test repo.
	code := Run([]string{"workspace", "commit", "-m", "land it", "--no-verify"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("commit exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got := headSubject(t, repo); got != "land it" {
		t.Fatalf("commit should execute by default; HEAD = %q, want %q", got, "land it")
	}
}

// Slice 2: --dry prints the plan and does NOT mutate.
func TestRun_Workspace_Commit_DryDoesNotMutate(t *testing.T) {
	repo := commitRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "commit", "-m", "land it", "--dry"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("commit --dry exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got := headSubject(t, repo); got != "seed" {
		t.Fatalf("--dry must NOT commit; HEAD = %q, want unchanged %q", got, "seed")
	}
	if !strings.Contains(out.String(), "--dry") && !strings.Contains(out.String(), "without --dry") {
		t.Errorf("dry-run should print the plan with a --dry hint:\n%s", out.String())
	}
}

func TestRun_Workspace_CwdVerb_RejectsPositionals(t *testing.T) {
	var out, errb bytes.Buffer
	// push is now cwd-only: any positional is a usage error pointing at the
	// cwd-only contract.
	code := Run([]string{"workspace", "push", "a", "b"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "cwd-only") {
		t.Fatalf("stderr should explain the cwd-only contract:\n%s", errb.String())
	}
}

func TestRun_Workspace_Help_ListsNewVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, verb := range []string{"create", "unclaim", "commit", "push", "submit", "status", "diff", "update", "prune", "merge"} {
		if !strings.Contains(out.String(), verb) {
			t.Errorf("workspace help missing %q:\n%s", verb, out.String())
		}
	}
}

func TestRun_Dev_NoSub_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"dev"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errb.String()), "usage") {
		t.Fatalf("stderr missing usage:\n%s", errb.String())
	}
}

func TestRun_Dev_Restart_BadService(t *testing.T) {
	var out, errb bytes.Buffer
	// disable sudo so this never tries real privilege if validation regressed
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	t.Setenv("APHROLLO_SYSTEMCTL", "/bin/false")
	code := Run([]string{"dev", "restart", "postgres"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage) for a disallowed service", code)
	}
	if !strings.Contains(errb.String(), "service not allowed") {
		t.Fatalf("stderr should reject the service:\n%s", errb.String())
	}
}

func TestRun_Dev_Restart_MissingArg(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"dev", "restart"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "restart") {
		t.Fatalf("stderr should show restart usage:\n%s", errb.String())
	}
}

func TestRun_Dev_UnknownSub(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"dev", "frob"}, strings.NewReader(""), &out, &errb); code != 2 {
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
