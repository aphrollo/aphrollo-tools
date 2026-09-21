package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedGateLogEntry writes one gate.log line by hand, at a chosen age.
// appendGateLog can only stamp "now", and a test about what a refusal SAYS
// about a verdict's age needs an age it did not have to wait for.
func seedGateLogEntry(t *testing.T, cfg, stage, root, verdict string, age time.Duration) {
	t.Helper()
	dir := filepath.Join(cfg, "gate-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("%s %s %s go test ./... %s 1.0s\n",
		time.Now().Add(-age).UTC().Format(time.RFC3339), stage, root, verdict)
	f, err := os.OpenFile(filepath.Join(dir, "gate.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// bashSuiteRoot makes a directory findRootFrom resolves as a project root,
// with no git and no suite actually runnable — DecideBashSuite never runs
// anything, so a bare go.mod marker is enough.
func bashSuiteRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module fixture\n\ngo 1.22\n")
	return dir
}

// decideBash runs DecideBashSuite over a Bash payload and fails the test if
// the payload was not judged at all — every case in this file names a
// command DecideBashSuite must recognise as a test-runner invocation.
func decideBash(t *testing.T, session, cwd, command string) Decision {
	t.Helper()
	d, judged := DecideBashSuite(bashPayload(t, session, cwd, command))
	if !judged {
		t.Fatalf("DecideBashSuite did not judge %q as a suite invocation", command)
	}
	return d
}

// An un-narrowed `go test ./...` beside a verdict the gate already holds for
// this tree answers nothing new: denying it is the whole point of the hook.
func TestDecideBashSuite_DeniesUnnarrowedWholeSuiteWithAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Block {
		t.Fatalf("want Block beside a fresh green verdict, got %v (reason %q)", d.Action, d.Reason)
	}
	if d.Policy != "bash-whole-suite" {
		t.Fatalf("want policy bash-whole-suite, got %q", d.Policy)
	}
}

// With NOTHING logged for the tree there is no answer to be redundant
// against, so the same un-narrowed invocation must flow — refusing it here
// would leave a session with no way to get a first verdict at all.
func TestDecideBashSuite_AllowsWholeSuiteWhenNoFreshVerdictExists(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Allow {
		t.Fatalf("want Allow with no verdict on record, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The sanctioned escape after an inconclusive TIMEOUT/SKIPPED line is a
// narrowed rerun: the code was not tested, so the rerun is the only way to
// an answer and must never be refused. The recent (inconclusive) line is
// what makes the run notable enough to count.
func TestDecideBashSuite_AllowsANarrowedRerunAfterAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "timeout", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./...")
	if d.Action != Allow {
		t.Fatalf("a -run-narrowed rerun must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// cargo test's own narrowing shape (-p <crate> <filter>) must stay allowed
// too after an inconclusive verdict — the classifier is not go-test-only.
// (The verdict seeded here was a green until issue #572 made a narrowed
// rerun beside a SETTLED verdict a block; what this case is about is the
// shape, so it now stands beside the inconclusive verdict the escape exists
// for.)
func TestDecideBashSuite_AllowsACargoPackageNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo test", "timeout", 0)

	d := decideBash(t, "s1", root, "cargo test -p widgets widget_roundtrip")
	if d.Action != Allow {
		t.Fatalf("a -p-narrowed cargo test must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// cargo nextest run's own -p narrowing must stay allowed too, matching
// cargo test's — same inconclusive-verdict standing, for the same reason.
func TestDecideBashSuite_AllowsACargoNextestPackageNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo nextest run", "queued-skipped", 0)

	d := decideBash(t, "s1", root, "cargo nextest run -p widgets")
	if d.Action != Allow {
		t.Fatalf("a -p-narrowed cargo nextest run must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// A bare `cargo nextest run` with no package or filter is the same
// whole-suite shape as `go test ./...` and must be denied on the same terms.
func TestDecideBashSuite_DeniesUnnarrowedCargoNextestWithAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("precommit", root, "cargo nextest run", "red", 0)

	d := decideBash(t, "s1", root, "cargo nextest run")
	if d.Action != Block {
		t.Fatalf("want Block, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The deny message is the whole point: a bare refusal invites a reworded
// resubmission, so it must name the verdict, its stage and the commands that
// answer what the line does not, not just say no. Every command it names has
// to be one this binary still has: `gate mutants status` was in this message
// after the verb was deleted, so a session following the advice got exit 2
// and a usage line.
func TestDecideBashSuite_DenyReasonNamesTheExistingVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	if strings.Contains(d.Reason, "gate mutants status") {
		t.Fatalf("deny reason %q names a verb that no longer exists", d.Reason)
	}
	for _, want := range []string{"green", "postedit", "aphrollo gate stats", "aphrollo gate status"} {
		if !strings.Contains(d.Reason, want) {
			t.Fatalf("deny reason %q must name %q", d.Reason, want)
		}
	}
}

// A deliberate soak is rare and expensive by nature, so every use is
// counted — like queue-bypass — regardless of what else is on record.
func TestDecideBashSuite_AllowsAndCountsADeliberateSoak(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	raw := bashPayload(t, "s1", root, "SOAK_SECS=600 go test ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a soak-marked go test must still be judged")
	}
	if d.Action != Allow {
		t.Fatalf("a soak must stay allowed, got %v", d.Action)
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-soak")
}

// An allowed narrowed rerun that runs beside a fresh verdict is exactly the
// escape hatch this hook must not let go uncounted — mirroring
// override-discard-env and queue-bypass, it gets its own gate.log line.
func TestDecideBashSuite_CountsANarrowedRerunBesideAFreshVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "timeout", 0)

	raw := bashPayload(t, "s1", root, "go test -run TestWidget ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a narrowed rerun must be judged")
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-narrowed")
}

// An ordinary narrowed rerun with nothing fresh on record is the mundane
// case (a session testing one function while writing it) and must NOT be
// counted, or every -run invocation would show up as an "override".
func TestDecideBashSuite_DoesNotCountAnOrdinaryNarrowedRerun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)

	raw := bashPayload(t, "s1", root, "go test -run TestWidget ./...")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a narrowed rerun must still be judged")
	}
	LogBashSuiteDecision(raw, d)
	if _, err := os.Stat(filepath.Join(cfg, "gate-state", "gate.log")); err == nil {
		t.Fatalf("an ordinary narrowed rerun must not write gate.log:\n%s", gateLogText(t, cfg))
	}
}

// PowerShell carries the same command shape under a different tool name
// (bashLikeTools, primary.go) and must be judged exactly like Bash.
func TestDecideBashSuite_JudgesPowerShellLikeBash(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d, judged := DecideBashSuite(powerShellPayload(t, "s1", root, "go test ./..."))
	if !judged {
		t.Fatal("a PowerShell whole-suite invocation must be judged")
	}
	if d.Action != Block {
		t.Fatalf("want Block for PowerShell too, got %v", d.Action)
	}
}

// An ordinary command that names no test runner at all is not this hook's
// concern and must not be judged (and therefore never logged).
func TestDecideBashSuite_IgnoresCommandsThatAreNotTestRunners(t *testing.T) {
	_, judged := DecideBashSuite(bashPayload(t, "s1", t.TempDir(), "git status"))
	if judged {
		t.Fatal("a non-test command must not be judged at all")
	}
}

// TestDecideBashSuite_DeniesANarrowedRerunBesideAFreshGreen pins issue #572:
// a narrowing was an unconditional pass, logged override-bash-narrowed 78
// times in seven days. The rule it was written for is "never refuse a rerun
// after an INCONCLUSIVE verdict"; beside a green the hook itself just
// delivered, the same rerun is exactly the redundant run the policy forbids.
func TestDecideBashSuite_DeniesANarrowedRerunBesideAFreshGreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./internal/tdd", "green", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Block {
		t.Fatalf("want Block for a narrowed rerun beside a fresh green, got %v (reason %q)", d.Action, d.Reason)
	}
}

// A fresh RED is just as settled as a green: the hook already said what is
// broken, and running one of its tests again by hand does not make that line
// any truer.
func TestDecideBashSuite_DeniesANarrowedRerunBesideAFreshRed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./internal/tdd", "red", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Block {
		t.Fatalf("want Block for a narrowed rerun beside a fresh red, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The safety case #572 must not touch: after an inconclusive verdict the code
// was NOT tested, and a narrowed rerun is the sanctioned way to get an answer
// at all. deferred-abandoned is the newest member of that family (issue
// #571), so it is the one asserted here.
func TestDecideBashSuite_AllowsANarrowedRerunAfterAnAbandonedDeferredJob(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./internal/tdd", DeferredAbandoned, 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Allow {
		t.Fatalf("a narrowed rerun after %s must stay allowed, got %v (reason %q)", DeferredAbandoned, d.Action, d.Reason)
	}
}

// The refusal has to hand the session the log line it should read instead:
// the verdict, and how long ago it was recorded. Without the age a session
// cannot tell an answer about the code it just wrote from one about the tree
// as it stood an hour back.
func TestDecideBashSuite_NarrowedDenyReasonNamesTheVerdictAndItsAge(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	seedGateLogEntry(t, cfg, "postedit", root, "green", 2*time.Minute)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Block {
		t.Fatalf("setup: want Block, got %v (reason %q)", d.Action, d.Reason)
	}
	// Minute granularity, not "2m0s": the age is measured from the seeded
	// entry to the moment the decision renders it, so a loaded runner that
	// takes a second to get there reports "2m1s" and a test pinned to the
	// second fails for a reason that has nothing to do with the rule.
	for _, want := range []string{"green", "logged 2m"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("deny reason %q must name %q", d.Reason, want)
		}
	}
}

// A marker that is the only way past a refusal stops meaning what it says:
// with the checkout bug open, three ordinary suite runs were labelled
// MUTATION=1 to get through and were then counted as mutation proofs
// (issue #645). The refusal has to name the honest routes first — the run's
// own text, and the inconclusive verdict a real rerun answers — and present
// the marker as what it is, a label that gets counted, not a bypass.
func TestDecideBashSuite_NarrowedDenyReasonNamesTheHonestRoutesBeforeTheMutationMarker(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Block {
		t.Fatalf("setup: want Block, got %v (reason %q)", d.Action, d.Reason)
	}
	output, marker := strings.Index(d.Reason, "gate output"), strings.Index(d.Reason, "MUTATION=1")
	if output < 0 || marker < 0 {
		t.Fatalf("deny reason must name both `gate output` and the marker:\n%s", d.Reason)
	}
	if output > marker {
		t.Errorf("the route to the run's own text must come before the marker:\n%s", d.Reason)
	}
	if !strings.Contains(d.Reason, "not a way past this refusal") {
		t.Errorf("deny reason must say the marker is not a bypass:\n%s", d.Reason)
	}
}

// An ignored-only nextest run (`--run-ignored ignored-only`) exercises tests
// a default run never touches (default nextest skips #[ignore]d tests
// entirely), so a fresh green logged from an ordinary run never answered for
// it. Allowed, and counted like a soak: this is a measurement, not a
// redundant rerun (issue #712).
func TestDecideBashSuite_AllowsAndCountsNextestIgnoredOnlyRun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo nextest run -p forge_lab", "green", 0)

	raw := bashPayload(t, "s1", root,
		"cargo nextest run -p forge_lab --release --run-ignored ignored-only "+
			"-E 'test(the_references_own_driven_wheel_tire_is_read_off_the_launch_logs)' --nocapture")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("an ignored-only nextest run must still be judged")
	}
	if d.Action != Allow {
		t.Fatalf("an ignored-only nextest run beside a fresh green must be allowed, got %v (reason %q)", d.Action, d.Reason)
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-measurement")
}

// cargo test's own ignored-only shape is `-- --ignored`, not nextest's
// `--run-ignored ignored-only` flag — same disjoint-test-set reasoning, same
// exemption.
func TestDecideBashSuite_AllowsAndCountsCargoTestIgnoredOnlyRun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo test -p widgets", "green", 0)

	raw := bashPayload(t, "s1", root, "cargo test -p widgets -- --ignored")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a cargo test --ignored run must still be judged")
	}
	if d.Action != Allow {
		t.Fatalf("a cargo test --ignored run beside a fresh green must be allowed, got %v (reason %q)", d.Action, d.Reason)
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-measurement")
}

// `--run-ignored all` runs ignored tests AND every normal one — it OVERLAPS
// the recorded verdict's test set, so the redundant-rerun objection genuinely
// applies and this must stay blocked. Only a filter that selects
// exclusively ignored tests is exempt.
func TestDecideBashSuite_BlocksNextestRunIgnoredAll(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo nextest run -p forge_lab", "green", 0)

	d := decideBash(t, "s1", root, "cargo nextest run -p forge_lab --run-ignored all")
	if d.Action != Block {
		t.Fatalf("--run-ignored all also selects non-ignored tests and must stay blocked, got %v (reason %q)", d.Action, d.Reason)
	}
}

// `--include-ignored` is cargo test's own "ignored AND normal" shape — same
// overlap, same block.
func TestDecideBashSuite_BlocksCargoTestIncludeIgnored(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo test -p widgets", "green", 0)

	d := decideBash(t, "s1", root, "cargo test -p widgets -- --include-ignored")
	if d.Action != Block {
		t.Fatalf("--include-ignored also selects non-ignored tests and must stay blocked, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The original #572 protection must stay exactly as strict for an ORDINARY
// narrowed rerun that names no ignored-test flag at all — the new exemption
// must not widen past what it names.
func TestDecideBashSuite_StillBlocksAnOrdinaryNarrowedRerunBesideAFreshGreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "cargo nextest run -p forge_lab", "green", 0)

	d := decideBash(t, "s1", root, "cargo nextest run -p forge_lab -E 'test(some_other_test)'")
	if d.Action != Block {
		t.Fatalf("an ordinary narrowed rerun beside a fresh green must stay blocked, got %v (reason %q)", d.Action, d.Reason)
	}
}

// The refusal text must not advertise the evasion route a real session took
// (worktree add --detach, hand-copy the test files, run there uncounted): it
// must name the verdict, its stage and age, but never say a different
// checkout goes unrefused.
func TestDecideBashSuite_NarrowedDenyReasonDoesNotNameADifferentCheckoutEscape(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./...", "green", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Block {
		t.Fatalf("setup: want Block, got %v (reason %q)", d.Action, d.Reason)
	}
	if strings.Contains(d.Reason, "different checkout") {
		t.Fatalf("deny reason must not name the different-checkout escape:\n%s", d.Reason)
	}
	for _, want := range []string{"green", "postedit"} {
		if !strings.Contains(d.Reason, want) {
			t.Fatalf("deny reason %q must still name %q", d.Reason, want)
		}
	}
}

// A mutation proof IS a narrowed rerun beside a fresh green by construction
// (mutate the code, run the one test, expect it to fail), and this repo
// sanctions it explicitly. Marked like a soak, allowed like a soak, and
// counted like a soak — an escape nobody can see is an escape nobody can
// judge.
func TestDecideBashSuite_AllowsAndCountsAMarkedMutationProof(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := bashSuiteRoot(t)
	appendGateLog("postedit", root, "go test ./internal/tdd", "green", 0)

	raw := bashPayload(t, "s1", root, "MUTATION=1 go test -run TestWidget ./internal/tdd")
	d, judged := DecideBashSuite(raw)
	if !judged {
		t.Fatal("a marked mutation proof must still be judged")
	}
	if d.Action != Allow {
		t.Fatalf("a marked mutation proof must stay allowed, got %v (reason %q)", d.Action, d.Reason)
	}
	LogBashSuiteDecision(raw, d)
	requireLoggedVerdict(t, cfg, "override-bash-mutation-proof")
}
