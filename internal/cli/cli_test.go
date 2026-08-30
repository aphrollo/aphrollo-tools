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
	if !strings.Contains(string(data), `/usr/local/bin/aphrollo\" tdd pretooluse`) {
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
	if strings.Contains(string(data), "tdd pretooluse") {
		t.Errorf("uninstall left hooks behind:\n%s", data)
	}
}

// `tdd init` (no --no-git) also installs the git gate: shims plus a global
// core.hooksPath. One command sets up everything.
func TestRun_TDDInit_GitGate(t *testing.T) {
	isolateGit(t)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	// An explicit --cargo-shim-dir, same reasoning as --git-hooks-dir: the
	// fake /usr/local/bin/aphrollo --bin below has no real directory on
	// this box, and cargo-shim install (task A7) derives its output dir
	// from --bin by default -- on Windows that drive-relative fake path
	// resolved to a REAL stray directory (D:/usr/local/bin/cargo-queue/)
	// before this override was added.
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--bin", "/usr/local/bin/aphrollo"},
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

// `tdd init` also installs the cargo-queue shim (task A7) next to --bin:
// a session can prepend that dir to its OWN PATH so a direct `cargo`
// invocation queues behind the same machine-wide build lock the hooks/gates
// use. --uninstall deliberately leaves it in place (see InstallCargoShim's
// doc comment).
func TestRun_TDDInit_CargoShim(t *testing.T) {
	isolateGit(t)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	shimDir := filepath.Join(t.TempDir(), "cargo-queue")
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")

	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--bin", bin},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstderr: %s", code, errb.String())
	}

	cmdData, err := os.ReadFile(filepath.Join(shimDir, "cargo.cmd"))
	if err != nil {
		t.Fatalf("cargo.cmd not written: %v", err)
	}
	if !strings.Contains(string(cmdData), bin) {
		t.Errorf("cargo.cmd missing the resolved bin path:\n%s", cmdData)
	}
	if _, err := os.ReadFile(filepath.Join(shimDir, "cargo")); err != nil {
		t.Fatalf("cargo (sh) not written: %v", err)
	}

	// --uninstall must NOT remove the cargo-shim files.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--uninstall"},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(shimDir, "cargo.cmd")); err != nil {
		t.Errorf("cargo-shim files must survive --uninstall, got: %v", err)
	}
}

// `tdd prepush` is a mechanical-only no-op: it exits 0 and never blocks, so a
// pre-push shim present on a box can never wedge a push. It must NOT depend on
// being inside a git repo or on any external reviewer — it returns immediately.
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

// `tdd premergecommit` outside a git repo must be a pure no-op (exit 0, no
// git/repo work attempted) — the same "not in a repo, nothing to gate" rule
// precommit follows.
func TestRun_TDDPremergecommit_NoOpOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "premergecommit"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("premergecommit exit = %d, want 0 outside a repo\nstderr: %s", code, errb.String())
	}
}

// `tdd premergecommit` on a real repo dispatches to Mechanical, not
// Precommit: a docs/plain-text-only staged change (no source or test file)
// exits 0 and reports "nothing to test" on stderr — proving the subcommand
// is actually wired up, not merely a no-op stub like prepush.
func TestRun_TDDPremergecommit_DocsOnlyIsNoOpWithMessage(t *testing.T) {
	commitRepo(t) // builds a repo with a staged new.txt and chdir's into it
	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "premergecommit"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("premergecommit exit = %d, want 0 for a docs-only merge\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "nothing to test") {
		t.Fatalf("expected a 'nothing to test' line on stderr, got:\n%s", errb.String())
	}
}

// TestRun_TDDCargo_DispatchesToShim proves `aphrollo tdd cargo ...` is
// actually wired through Run's subcommand switch to the shim, not merely
// tested at runCargoShim's own level — an uncontended lock, exit code
// propagated from the (stubbed) real cargo, and silence on stderr.
func TestRun_TDDCargo_DispatchesToShim(t *testing.T) {
	withIsolatedCargoLock(t)
	t.Setenv("APHROLLO_REAL_CARGO", stubCargo())

	var out, errb bytes.Buffer
	code := Run(append([]string{"tdd", "cargo"}, stubCargoArgsExit(3)...), strings.NewReader(""), &out, &errb)
	if code != 3 {
		t.Fatalf("tdd cargo exit = %d, want 3 (propagated from the stub)", code)
	}
	if errb.Len() != 0 {
		t.Fatalf("an uncontended tdd cargo run must print nothing to stderr, got: %q", errb.String())
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

func TestRun_Help_ListsDocs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "docs") {
		t.Fatalf("root usage should list docs:\n%s", out.String())
	}
}

func TestRun_Docs_Help(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"docs", "--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("`docs --help` should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "check") || !strings.Contains(out.String(), "unresolved reference") {
		t.Errorf("docs help should document `check` and its report format:\n%s", out.String())
	}
}

func TestRun_Docs_NoSub_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"docs"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("bare `docs` should be a usage error (2), got %d", code)
	}
	if !strings.Contains(errb.String(), "check") {
		t.Errorf("usage should list check:\n%s", errb.String())
	}
}

func TestRun_Docs_UnknownSub(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"docs", "frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("unknown docs subcommand should be 2, got %d", code)
	}
}

// gitInit makes a throwaway repo with the given files (relative path → content),
// committing them so `git ls-files` sees them tracked.
func gitInit(t *testing.T, files map[string]string) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestRun_Docs_Check_Clean(t *testing.T) {
	dir := gitInit(t, map[string]string{
		"README.md":     "see `internal/x.go` and [guide](docs/guide.md)\n",
		"internal/x.go": "package x\n",
		"docs/guide.md": "# guide\n",
	})
	var out, errb bytes.Buffer
	code := Run([]string{"docs", "check", dir}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("clean repo should exit 0, got %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
}

func TestRun_Docs_Check_ReportsDangling(t *testing.T) {
	dir := gitInit(t, map[string]string{
		"README.md":     "good `internal/x.go` but [gone](docs/removed.md)\n",
		"internal/x.go": "package x\n",
	})
	var out, errb bytes.Buffer
	code := Run([]string{"docs", "check", dir}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("dangling ref should exit 1, got %d", code)
	}
	if !strings.Contains(out.String(), "README.md:1: unresolved reference: docs/removed.md") {
		t.Errorf("expected the dangling ref reported:\n%s", out.String())
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

// prunableWorktree builds a temp repo with one linked worktree at the default
// layout (<parent>/.worktrees/<repo>/<slug>) and chdir's to the parent (outside
// the worktree, so the cwd-guard never trips). Returns the repo, the worktree
// path, and the branch checked out there.
func prunableWorktree(t *testing.T) (repo, wt, branch string) {
	t.Helper()
	parent := t.TempDir()
	repo = filepath.Join(parent, "myrepo")
	branch = "feat/x"
	run := func(dir string, args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "config", "user.email", "t@t")
	run(repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-qm", "seed")
	wt = filepath.Join(parent, ".worktrees", "myrepo", "feat-x")
	run(repo, "worktree", "add", "-q", "-b", branch, wt, "main")
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	return repo, wt, branch
}

// `workspace prune <repo> <branch>` removes that one ticket's worktree and is
// safe to re-run: the second call (worktree already gone) is still exit 0 with an
// "already gone" receipt, never an error.
func TestRun_Workspace_Prune_Ticket_Idempotent(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("prune exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("prune should remove the ticket worktree, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "pruned") {
		t.Errorf("receipt should report the pruned worktree:\n%s", out.String())
	}

	var out2, errb2 bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch}, strings.NewReader(""), &out2, &errb2); code != 0 {
		t.Fatalf("idempotent re-run exit = %d, want 0\nstderr: %s", code, errb2.String())
	}
	if !strings.Contains(out2.String(), "already gone") {
		t.Errorf("re-run should report the worktree already gone:\n%s", out2.String())
	}
}

// `workspace prune <repo> <branch> --dry` previews without removing.
func TestRun_Workspace_Prune_Ticket_DryDoesNotRemove(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch, "--dry"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("prune --dry exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("--dry must NOT remove the worktree: %v", err)
	}
	if !strings.Contains(out.String(), "would prune") {
		t.Errorf("dry-run should preview the prune:\n%s", out.String())
	}
}

// Too many positionals is a usage error naming both prune forms.
func TestRun_Workspace_Prune_TooManyArgs(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "prune", "a", "b", "c"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "prune") {
		t.Fatalf("usage error should name prune:\n%s", errb.String())
	}
}

// Help documents the per-ticket prune form and that submit is per-worktree (one
// repo at a time) — the two contracts this ticket pins.
func TestRun_Workspace_Help_DocumentsPerTicketPruneAndPerRepoSubmit(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "prune <repo> <branch>") {
		t.Errorf("help should document the per-ticket prune form:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "one repo at a time") {
		t.Errorf("help should state submit is per-worktree (one repo at a time):\n%s", out.String())
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

// An unwritable cargo-shim dir must NOT fail `tdd init`. The shims are an
// opt-in convenience (a session prepends the dir to its own PATH); the two
// things init exists for — the session hooks and the git gate — are the
// contract. Exiting non-zero after both succeeded aborts whatever drives
// init, which is how a deploy-infra apply died on
// `mkdir /usr/local/bin/cargo-queue: permission denied` when the task ran as
// an unprivileged user against a system-wide --bin.
func TestRun_TDDInit_UnwritableShimDir_WarnsButSucceeds(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	isolateGit(t)
	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")

	// A read-only parent, so creating the shim dir under it is denied.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	shimDir := filepath.Join(parent, "cargo-queue")

	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "init", "--config-dir", cfg, "--git-hooks-dir", hooks, "--cargo-shim-dir", shimDir, "--bin", bin},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("init exit = %d, want 0 — an unwritable shim dir must not fail init\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "wired session hooks") {
		t.Errorf("session hooks not reported as wired:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "installed git gate") {
		t.Errorf("git gate not reported as installed:\n%s", out.String())
	}
	// The warning has to name the dir and the flag, or the operator cannot act on it.
	if !strings.Contains(errb.String(), shimDir) || !strings.Contains(errb.String(), "--cargo-shim-dir") {
		t.Errorf("warning must name the dir and --cargo-shim-dir:\n%s", errb.String())
	}
}
