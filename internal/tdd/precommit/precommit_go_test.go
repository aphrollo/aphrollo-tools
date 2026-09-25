package precommit

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// driftVersionCounter makes each call to newDriftVersionPair pick a version
// pair no earlier call in this PROCESS has used. driftNoted (precommit_go.go)
// dedupes its log line by (pinned, local) for the process's whole life, by
// design — so a test that reused one fixed pair would log once ever, and
// every later -count=N rerun in the same process would find the pair already
// noted and silently skip the write.
var driftVersionCounter int32

// newDriftVersionPair returns a pinned/local version pair that always
// differs from each other and from every earlier call in this process.
func newDriftVersionPair() (pinned, local string) {
	n := atomic.AddInt32(&driftVersionCounter, 1)
	return fmt.Sprintf("2.12.%d", n), fmt.Sprintf("2.9.%d", n)
}

func withLinter(t *testing.T, present bool) {
	t.Helper()
	t.Cleanup(SetLookLinterForTest(func() bool { return present }))
}

func withLinterVersion(t *testing.T, version string) {
	t.Helper()
	t.Cleanup(SetLinterVersionForTest(func(string) string { return version }))
}

func runsAt(seen *[]Runner, root string) SuiteRunner {
	return tddtest.RecordRunner(seen, root, nil, SuiteResult{Passed: true})
}

func cmdLine(r Runner) string { return strings.TrimSpace(r.Cmd + " " + strings.Join(r.Args, " ")) }

// ratchet: test_removed TestPrecommitGoRootRunsVetThenLintThenTheSuite: the commit gate no longer runs a suite, so the third element of its want is gone and the claim is a different one; restated below as TestPrecommitGo_RootRunsVetThenLintAndStopsThere.

// CI runs vet and the linter; a gate that does not runs a different check
// from the one that decides whether the branch is green. Order is the cost
// order: vet compiles nothing extra, lint is a full analysis pass. Nothing
// follows them at commit time: a suite reappearing here is the break this
// test catches, and it is the merge gate's job now.
func TestPrecommitGo_RootRunsVetThenLintAndStopsThere(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	var order []string
	for _, r := range seen {
		order = append(order, cmdLine(r))
	}
	// The suite is absent by design: vet and lint are the commit gate's
	// remaining Go stages, and the suite runs at the merge.
	want := []string{"go vet ./...", "golangci-lint run --allow-serial-runners ."}
	if strings.Join(order, " | ") != strings.Join(want, " | ") {
		t.Fatalf("stages ran %v, want %v", order, want)
	}
}

// golangci-lint is a full analysis pass (unlike vet, which compiles nothing
// extra), so it is the one CI-parity check worth SCOPING: two staged files in
// two different packages must lint only those two packages, never fall back
// to ./... and re-analyze the whole module for a two-file commit.
func TestPrecommitLint_scopesToTheTouchedPackagesNotTheWholeModule(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "sub/thing.go", "package sub\n\nfunc Thing() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	for _, r := range seen {
		if r.Cmd != golangciLint {
			continue
		}
		want := []string{"run", "--allow-serial-runners", ".", "./sub"}
		if strings.Join(r.Args, " ") != strings.Join(want, " ") {
			t.Fatalf("lint argv = %v, want %v", r.Args, want)
		}
		return
	}
	t.Fatalf("the linter never ran: %+v", seen)
}

// golangci-lint takes a MACHINE-WIDE lock, not one per cache dir, so a second
// one running anywhere on the box makes this one exit 3 with "parallel
// golangci-lint is running" — a rejection that says nothing about the code.
// CI passes --allow-serial-runners for exactly this reason; the gate, which
// runs while other sessions build, needs it more.
func TestPrecommitPassesAllowSerialRunnersToTheLinter(t *testing.T) {
	withLinter(t, true)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	for _, r := range seen {
		if r.Cmd != golangciLint {
			continue
		}
		if !slices.Contains(r.Args, "--allow-serial-runners") {
			t.Fatalf("lint argv = %v, want --allow-serial-runners", r.Args)
		}
		return
	}
	t.Fatalf("the linter never ran: %+v", seen)
}

// The gate exists to run what CI runs. A local binary at a different version
// answers a different question, and finding that out from a red CI job after
// a green commit is the whole failure this gate is for — so it is said out
// loud. It is NOT a rejection: a version mismatch is not a defect in the
// code, and refusing the commit would wedge every box that has not upgraded.
func TestPrecommitLogsLintVersionDriftAndStillRuns(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withLinter(t, true)
	pinned, local := newDriftVersionPair()
	withLinterVersion(t, local)
	root := makeGoRepo(t)
	write(t, root, ".github/workflows/pipeline.yml",
		fmt.Sprintf("jobs:\n  lint:\n    steps:\n      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v%s\n", pinned))
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("drift must never reject a commit: %s", res.Message)
	}
	ran := false
	for _, r := range seen {
		ran = ran || r.Cmd == golangciLint
	}
	if !ran {
		t.Fatalf("the linter must still run on drift: %+v", seen)
	}
	requireLoggedVerdict(t, cfg, "lint-version-drift")
}

func TestPrecommitIsQuietWhenTheLinterMatchesTheWorkflowPin(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withLinter(t, true)
	withLinterVersion(t, "2.12.2")
	root := makeGoRepo(t)
	write(t, root, ".github/workflows/pipeline.yml",
		"jobs:\n  lint:\n    steps:\n      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	Precommit(root, runsAt(&seen, root))
	if strings.Contains(gateLogText(t, cfg), "lint-version-drift") {
		t.Fatal("a matching version is not drift")
	}
}

// A linter nobody installed is not a failing commit. It is one log line, so
// the difference between "clean" and "never ran" stays visible.
func TestPrecommitSkipsTheLinterWhenItIsNotOnPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withLinter(t, false)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, runsAt(&seen, root))
	if res.Blocked {
		t.Fatalf("an absent linter must never reject a commit: %s", res.Message)
	}
	for _, r := range seen {
		if r.Cmd == "golangci-lint" {
			t.Fatalf("the linter is not installed, so it must not be run: %v", seen)
		}
	}
	requireLoggedVerdict(t, cfg, "lint-skipped")
}

// The linter that IS installed and finds something rejects, and the rejection
// names the command so it can be reproduced.
func TestPrecommitRejectsWhenTheLinterFails(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		if r.Cmd == "golangci-lint" {
			return SuiteResult{Passed: false, Output: "widget.go:3:6: `Widget` is unused (unused)\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("a lint failure must reject the commit")
	}
	for _, want := range []string{"golangci-lint run --allow-serial-runners .", "Widget"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

// golangci-lint's own machine-wide lock (still reachable when a lint runs
// outside this gate's control — an operator's own shell, or a CI job that
// somehow got past the box-wide lint lock) reports contention with the exact
// text "parallel golangci-lint is running" and exit 3, carrying no file, no
// line, no diagnostic naming anything about the code. Reporting that as
// "TDD quality: lint failed... fix before committing" sends the author
// hunting for a bug that was never linted; the same commit passes clean on
// retry once the box is no longer contended — exactly the defect observed
// 2026-09-23 across two lanes' commit gates. Contention must be its own
// outcome: still refuses the commit (lint never actually judged the code),
// but says so as a retry, never a lint verdict.
func TestPrecommit_ClassifiesLintContentionAsNotALintFailure(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		if r.Cmd == "golangci-lint" {
			return SuiteResult{Passed: false, Output: "Error: parallel golangci-lint is running\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("lint that never actually ran must still refuse the commit")
	}
	if strings.Contains(res.Message, "lint failed") || strings.Contains(res.Message, "fix before committing") {
		t.Fatalf("box contention reported as a lint failure: %q", res.Message)
	}
	if !strings.Contains(res.Message, "retry") && !strings.Contains(res.Message, "Retry") {
		t.Fatalf("message %q names no retry remedy", res.Message)
	}
}

// A dangling citation in a doc misdrives every session that loads it, and a
// docs-only commit stages no source at all — so the check has to run before
// the has-code gate, not inside a per-root suite stage.
func TestPrecommitChecksStagedMarkdownOnADocsOnlyCommit(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "NOTES.md", "see [the plan](docs/nowhere.md)\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	if !res.Blocked {
		t.Fatalf("a dangling citation must reject: %+v", res)
	}
	for _, want := range []string{"NOTES.md", "docs/nowhere.md"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

func TestPrecommitPassesStagedMarkdownThatResolves(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "docs/plan.md", "# plan\n")
	write(t, root, "NOTES.md", "see [the plan](docs/plan.md)\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); res.Blocked {
		t.Fatalf("a citation that resolves must pass: %s", res.Message)
	}
}

// A repo that is neither a Go module nor opted in never sees the stage: the
// doc conventions it enforces are not universal.
func TestPrecommitSkipsDocsCheckForARepoThatDidNotOptIn(t *testing.T) {
	root := makeJSRepo(t, `{"name":"x","scripts":{"test":"vitest run"}}`)
	write(t, root, "NOTES.md", "see [the plan](docs/nowhere.md)\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); res.Blocked {
		t.Fatalf("an opt-out repo must not be judged on doc citations: %s", res.Message)
	}

	// The marker opts it in, and then the same commit is refused.
	write(t, root, filepath.Join(".aphrollo", "docs-check"), "")
	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); !res.Blocked {
		t.Fatalf(".aphrollo/docs-check must turn the stage on: %+v", res)
	}
}

// A cargo workspace says it in the manifest, beside every other gate opt-in,
// rather than growing a second place to look.
func TestPrecommitReadsDocsCheckFromTheWorkspaceManifest(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\ndocs-check = true\n")
	write(t, root, "NOTES.md", "see [the plan](docs/nowhere.md)\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	if !res.Blocked || !strings.Contains(res.Message, "docs/nowhere.md") {
		t.Fatalf("docs-check = true must turn the stage on: %+v", res)
	}
}

// The workflow pin is read out of the lint action's own `with:` block. The
// pattern used to accept the first `version:` ANYWHERE after the action line,
// and `go-version:` in the setup-go step right below it matched -- so the
// gate compared the linter against a Go toolchain version and reported drift
// on every commit.
func TestPinnedLinterVersion_ReadsTheActionsOwnVersionNotTheStepBelowIt(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".github", "workflows", "pipeline.yml"), `jobs:
  lint:
    steps:
      - uses: golangci/golangci-lint-action@v6
        with:
          version: v2.12.2
      - uses: actions/setup-go@v5
        with:
          go-version: 1.25.1
`)

	if got := pinnedLinterVersion(root); got != "2.12.2" {
		t.Fatalf("pinnedLinterVersion = %q, want the lint action's own pin", got)
	}
}

// The same file with the steps the other way round: a `go-version:` BEFORE
// the action must not be read either, and the action's own key still is.
func TestPinnedLinterVersion_IgnoresAGoVersionAboveTheLintAction(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".github", "workflows", "pipeline.yml"), `jobs:
  lint:
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: 1.25.1
      - uses: golangci/golangci-lint-action@v6
        with:
          version: v2.12.2
`)

	if got := pinnedLinterVersion(root); got != "2.12.2" {
		t.Fatalf("pinnedLinterVersion = %q, want the lint action's own pin", got)
	}
}

// An action step that pins nothing is unpinned: reading the next step's
// version would invent a pin the workflow does not state — and the gate would
// then report drift on every commit against a number that is not a linter
// version at all.
func TestPinnedLinterVersion_IsEmptyWhenTheActionPinsNothing(t *testing.T) {
	for _, next := range []string{"go-version: 1.25.1", "version: 1.36.0"} {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, ".github", "workflows", "pipeline.yml"), `jobs:
  lint:
    steps:
      - uses: golangci/golangci-lint-action@v6
      - uses: extractions/setup-just@v2
        with:
          `+next+`
`)

		if got := pinnedLinterVersion(root); got != "" {
			t.Errorf("with %q below it, pinnedLinterVersion = %q, want none — the action states no version", next, got)
		}
	}
}

// Tests above precommit (the doctor's linter checks) stub the linter probes
// only through these setters, so each must install its stub and put the
// real probe back.
func TestLinterSetters_InstallAndRestore(t *testing.T) {
	realPresent, realVersion := lookLinter(), linterVersion(t.TempDir())

	restore := SetLookLinterForTest(func() bool { return !realPresent })
	if lookLinter() == realPresent {
		restore()
		t.Fatal("presence stub not installed")
	}
	restore()
	if lookLinter() != realPresent {
		t.Error("restore left the presence stub in place")
	}

	restore = SetLinterVersionForTest(func(string) string { return "9.9.9-stub" })
	if got := linterVersion(t.TempDir()); got != "9.9.9-stub" {
		restore()
		t.Fatalf("version stub not installed: %q", got)
	}
	restore()
	if got := linterVersion(t.TempDir()); got != realVersion {
		t.Errorf("restore left the version stub in place: %q, want %q", got, realVersion)
	}
}
