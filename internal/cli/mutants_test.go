package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The post-commit hook fires after EVERY commit on the box, including ones in
// repos that never heard of this tool. It must cost nothing and, above all,
// never fail: git prints a hook's failure to a session that has already
// committed, which reads as a broken commit.
func TestPostCommit_NeverFailsOutsideAGatedRepo(t *testing.T) {
	gateConfigDir(t)
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postcommit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("postcommit exit = %d, want 0 outside a repo\nstderr: %s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("postcommit wrote to stdout: %q", out.String())
	}
}

// A worktree the hook cannot prepare must be REPORTED, not silenced behind a
// line that claims the run started: the hook is the operator's only window
// into a mutation run that happens entirely off-screen from here on (issue
// #114).
func TestRunPostCommit_ReportsAWorktreePrepareFailure(t *testing.T) {
	gateConfigDir(t)
	isolateGitConfigCLI(t)
	// A runner genuinely low on disk would otherwise refuse the run for THAT
	// reason first, masking the prepare failure this test is actually about.
	t.Cleanup(tdd.SetFreeSpaceForTest(1000, true))
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"),
		[]byte("[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n"+
			"[workspace.metadata.aphrollo]\nmutation-receipt = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "opt in")
	run("checkout", "-q", "-b", "lane/x")
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	// Carries the `Mutants: run` trailer (issue #521): the post-commit path
	// only starts a job when the author asked for one.
	run("commit", "-qm", "lane work\n\nMutants: run")

	// Block the mutants worktree's own parent directory with a FILE, so its
	// mkdir fails.
	blocked := filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root))
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postcommit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("postcommit exit = %d, want 0 — a hook never fails the commit\nstderr: %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "started") {
		t.Fatalf("stderr = %q, want no claim of a run that never started", errb.String())
	}
	if !strings.Contains(errb.String(), "gate: mutation run failed to start:") {
		t.Fatalf("stderr = %q, want the failure reported by name", errb.String())
	}
}

// The detached wrapper is addressed by a job file. Pointed at one that is not
// there it used to exit 0 in silence, on the theory that a spawned process has
// nowhere to report — but its stdout IS the job log, which is exactly where a
// run that measured nothing needs to say so. Silence there is a receipt that
// never arrives for no stated reason.
func TestMutantsRun_ReportsAJobFileItCannotRead(t *testing.T) {
	gateConfigDir(t)
	missing := filepath.Join(t.TempDir(), "nope.json")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--job", missing}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("mutants run exit = 0 for a job file that is not there\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "nope.json") {
		t.Fatalf("the job log never names the file it could not read: %q", out.String())
	}
}

// `--jobs`/`--base`/`--timeout-multiplier`/`--minimum-test-timeout` typed on
// `gate mutants run` are the env-versus-flag rule's flag half: they set the
// override channel BEFORE the job runs, so a maintainer rerunning one job by
// hand gets the value typed, not whatever the session's environment carried.
func TestMutantsRun_FlagsSetTheOverrideEnvBeforeTheJobRuns(t *testing.T) {
	gateConfigDir(t)
	t.Setenv(tdd.MutantsJobsEnv, "")
	t.Setenv(tdd.MutantsBaseOverrideEnv, "")
	t.Setenv(tdd.MutantsTimeoutMultiplierEnv, "")
	t.Setenv(tdd.MutantsMinTestTimeoutEnv, "")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run",
		"--job", filepath.Join(t.TempDir(), "nope.json"),
		"--jobs", "4", "--base", "abc123", "--timeout-multiplier", "3", "--minimum-test-timeout", "20s",
	}, strings.NewReader(""), &out, &errb)
	// 2, not 0: the job file named here does not exist. What this test is
	// about is that the overrides were already set when the run was reached,
	// which a refusal downstream of that does not disturb.
	if code != 2 {
		t.Fatalf("mutants run exit = %d, want 2 for an unreadable job\nstderr: %s", code, errb.String())
	}
	if got := os.Getenv(tdd.MutantsJobsEnv); got != "4" {
		t.Errorf("%s = %q, want the --jobs flag's value", tdd.MutantsJobsEnv, got)
	}
	if got := os.Getenv(tdd.MutantsBaseOverrideEnv); got != "abc123" {
		t.Errorf("%s = %q, want the --base flag's value", tdd.MutantsBaseOverrideEnv, got)
	}
	if got := os.Getenv(tdd.MutantsTimeoutMultiplierEnv); got != "3" {
		t.Errorf("%s = %q, want the --timeout-multiplier flag's value", tdd.MutantsTimeoutMultiplierEnv, got)
	}
	if got := os.Getenv(tdd.MutantsMinTestTimeoutEnv); got != "20s" {
		t.Errorf("%s = %q, want the --minimum-test-timeout flag's value", tdd.MutantsMinTestTimeoutEnv, got)
	}
}

// `gate mutants go` has two callers with two addressing modes: the detached
// local job names a job FILE, and CI names the merge base its diff is scoped
// to. The routing is pinned here through the invocations that come back BEFORE
// the tool is spawned. Running the real thing from a unit test would start a
// mutation run of this whole module inside `go test` on any box that has
// gremlins on PATH — including the CI runner, which now installs it — and the
// old test only passed there because the binary was absent. That the runner
// fails when the tool wrote no report is internal/tdd's
// TestRunGoMutantsCI_FailsWhenTheToolWroteNoReport.
//
// The two exit codes are different answers and CI reads them as such: 2 is
// "you invoked it wrong", 1 is "the check failed".

// The base is not optional and has no default: an unscoped run measures the
// whole module. Only RunGoMutantsCI prints this line, so reaching it is also
// what proves --diff routes to the CI judge and not to the detached job.
func TestMutantsGo_DiffModeRefusesAnEmptyBase(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "go", "--diff", ""}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 — an empty base is a bad invocation, not a failed check", code)
	}
	if !strings.Contains(errb.String(), "merge base") {
		t.Fatalf("stderr = %q, want it to say the merge base is what is missing", errb.String())
	}
}

// --receipt names where the CI run leaves its proof, so it means nothing
// without --diff. It used to fall through to the detached job with an empty
// job path, which returns 0 — a green check that ran nothing, from a command
// line that looks like it asked for a run.
func TestMutantsGo_RefusesAReceiptPathWithNoDiff(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "go", "--receipt", filepath.Join(t.TempDir(), "r.json")},
		strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 — --receipt with no --diff ran nothing and said nothing", code)
	}
	if !strings.Contains(errb.String(), "--diff") {
		t.Fatalf("stderr = %q, want it to name the flag that is missing", errb.String())
	}
}

// The CI mode is a flag an operator has to be able to find. `gate mutants go`
// was documented as `run|go --job <file>` alone, so the only way to learn that
// --diff exists was to read the dispatch.
func TestGateUsage_DocumentsTheCIModeOfMutantsGo(t *testing.T) {
	for _, want := range []string{"go --diff <base>", "--receipt <path>", "--store <dir>"} {
		if !strings.Contains(gateUsage, want) {
			t.Errorf("gate usage does not carry %q — an operator cannot find the CI mode", want)
		}
	}
}

// --store <dir> is CI's outcome-cache override (issue #143), forwarded to
// GoMutantsCI.Store. Proven by the directory existing afterward:
// ciMutantStorePath creates it before anything else the run does, and
// gremlins itself is not installed in this sandbox, so the run fails fast
// right after — without a mock this is the one externally observable side
// effect that proves the flag reached the runner rather than being silently
// dropped by the flag set.
func TestMutantsGo_StoreFlagIsForwardedToTheOutcomeCache(t *testing.T) {
	gateConfigDir(t)
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	store := filepath.Join(t.TempDir(), "cache")
	var out, errb bytes.Buffer
	Run([]string{"gate", "mutants", "go", "--diff", "deadbeef", "--store", store},
		strings.NewReader(""), &out, &errb)

	if _, err := os.Stat(store); err != nil {
		t.Fatalf("--store %s was never created: %v — the flag did not reach GoMutantsCI.Store", store, err)
	}
}

// `--jobs`/`--base`/`--timeout-multiplier`/`--minimum-test-timeout` are
// `run`'s own override channel (env-versus-flag rule, above): they are
// wired into RunMutantsJob only. On `go` they used to parse without error
// and then do nothing — silent no-ops nobody typing them would notice,
// since the concurrency GoMutantsCI actually asks for comes from a
// per-box formula unless the caller names an explicit --diff-mode base,
// which none of these four flags are. Declaring them only on `run`'s own
// flagset (mirroring how --diff/--receipt/--store are scoped to `go`
// only) turns a mistyped one into a real flag.Parse error instead.
func TestMutantsGo_RefusesRunsOwnFlags(t *testing.T) {
	gateConfigDir(t)
	for _, args := range [][]string{
		{"gate", "mutants", "go", "--diff", "deadbeef", "--jobs", "4"},
		{"gate", "mutants", "go", "--diff", "deadbeef", "--base", "abc123"},
		{"gate", "mutants", "go", "--diff", "deadbeef", "--timeout-multiplier", "3"},
		{"gate", "mutants", "go", "--diff", "deadbeef", "--minimum-test-timeout", "20s"},
	} {
		var out, errb bytes.Buffer
		code := Run(args, strings.NewReader(""), &out, &errb)
		if code != 2 {
			t.Errorf("Run(%v) exit = %d, want 2 — `run`'s own flag typed on `go` must be refused, not silently ignored\nstderr: %s",
				args, code, errb.String())
		}
	}
}

// A verb this binary does not have must say so rather than silently doing
// nothing — a mistyped subcommand that exits 0 is a hook that never ran.
func TestMutants_RejectsAnUnknownVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "frobnicate"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatal("an unknown mutants verb must not exit 0")
	}
	if !strings.Contains(errb.String(), "frobnicate") {
		t.Fatalf("stderr = %q, want it to name the verb", errb.String())
	}
}

// GITHUB_ACTIONS=true is the one explicit signal `go --diff` reads to set
// GoMutantsCI.OneJobPerContainer (issue #406) — set by GitHub's own runner
// for every job it starts, never inferred from anything about the
// invocation itself.
func TestRunningOnHostedCIRunner_ReadsExactlyTheGitHubActionsSignal(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	if !runningOnHostedCIRunner() {
		t.Fatal("GITHUB_ACTIONS=true must read as a hosted CI runner")
	}
	t.Setenv("GITHUB_ACTIONS", "")
	if runningOnHostedCIRunner() {
		t.Fatal("an unset GITHUB_ACTIONS must not read as a hosted CI runner — a developer's own shell must take the run lock")
	}
}

// `audit` with no --package must be a usage error routed to
// tdd.RunMutantsAudit — proven here through the dispatch, not by testing
// RunMutantsAudit's own body again.
func TestMutantsAudit_RefusesWithNoPackageFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "audit"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("mutants audit with no --package exit = %d, want 2\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--package") {
		t.Fatalf("stderr = %q, want it to name the missing flag", errb.String())
	}
}

// A flag `audit` does not have is a real parse error, not a silent ignore.
func TestMutantsAudit_RejectsAnUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "audit", "--bogus"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("mutants audit --bogus exit = %d, want 2\nstderr: %s", code, errb.String())
	}
}

// The verb has to be findable from the usage text the way every other one is.
func TestGateUsage_DocumentsTheAuditVerb(t *testing.T) {
	for _, want := range []string{"audit --package <name>", "file:line: mutation"} {
		if !strings.Contains(mutantsUsage, want) {
			t.Errorf("mutantsUsage does not carry %q — an operator cannot find the audit verb", want)
		}
	}
}
