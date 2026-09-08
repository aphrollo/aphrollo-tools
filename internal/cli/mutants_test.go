package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
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

// There is no concurrency to override any more: a Cargo measurement is
// in-place, cargo-mutants refuses `--jobs` beside `--in-place`, and a flag
// that quietly accepted a number would be promising something the tool will
// not do (issue #592). The flag package's own "not defined" message is the
// answer, and the run stops before it measures anything.
func TestGateMutantsRun_RejectsAJobsFlag(t *testing.T) {
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
	code := Run([]string{"gate", "mutants", "run", "--jobs", "3"}, strings.NewReader(""), &out, &errb)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage) for a flag this command does not have\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "flag provided but not defined: -jobs") {
		t.Fatalf("stderr = %q, want the flag package's own refusal naming -jobs", errb.String())
	}
}

// ratchet: test_removed TestRunPostCommit_ReportsAWorktreePrepareFailure: post-commit no longer starts anything, so there is no worktree for it to fail to prepare
// ratchet: test_removed TestMutantsRun_ReportsAJobFileItCannotRead: `run --job` is deleted with the detached job; `run` measures the checkout it is typed in
// ratchet: test_removed TestMutantsRun_FlagsSetTheOverrideEnvBeforeTheJobRuns: the override env existed to reach a detached job's process; --base and --jobs are now arguments to MeasureLane, proved by TestGateMutantsRun_BaseDefaultsToMergeBaseWithDefaultBranch
// ratchet: test_removed TestMutantsGo_DiffModeRefusesAnEmptyBase: `go` is deleted; a Go repo is measured by `run` through the same code path a Cargo one is
// ratchet: test_removed TestMutantsGo_RefusesAReceiptPathWithNoDiff: there is no receipt path to name
// ratchet: test_removed TestGateUsage_DocumentsTheCIModeOfMutantsGo: nightly CI calls `run --base <sha>`, documented in mutantsUsage
// ratchet: test_removed TestMutantsGo_StoreFlagIsForwardedToTheOutcomeCache: there is no outcome cache
// ratchet: test_removed TestMutantsGo_RefusesRunsOwnFlags: there is no second flagset to disagree with
// ratchet: test_removed TestRunningOnHostedCIRunner_ReadsExactlyTheGitHubActionsSignal: OneJobPerContainer belonged to the CI half of `go`; the jobs cap is now one formula for every caller
// ratchet: test_removed TestMutantsAudit_RefusesWithNoPackageFlag: `audit` measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestMutantsAudit_RejectsAnUnknownFlag: same deletion
// ratchet: test_removed TestGateUsage_DocumentsTheAuditVerb: same deletion
// ratchet: test_removed TestGateMutantsStatus_OutsideARepoExitsWithTheErrorCode: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestGateMutantsStatus_NoneWhenNothingHasEverMeasuredThisTree: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestGateMutantsStatus_UnknownFlagExitsUsage: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestGateMutants_HelpDocumentsStatusAndItsExitCodes: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestReceiptSign_SignsTheFileItIsGiven: `gate receipt sign` is deleted; nothing signs a document any more
// ratchet: test_removed TestReceiptSign_FailsLoudlyOnAFileItCannotSign: `gate receipt sign` is deleted; nothing signs a document any more
