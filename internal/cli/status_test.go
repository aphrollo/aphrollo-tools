package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestGateStatus_NothingGoingOn_StillPrintsEverySection is the plain,
// nothing-going-on case for `aphrollo gate status` (issue #430): it must
// still print every section, each saying explicitly that it found nothing,
// rather than a silent or partial report.
// ratchet: test_removed TestGateStatus_NoDeferredNoBuildNoMutants_StillPrintsAllThreeSections: renamed with the mutation-run section it named, which is deleted with the detached run it reported on; the remaining sections are asserted unchanged
func TestGateStatus_NothingGoingOn_StillPrintsEverySection(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "status"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (status is read-only and never fails the session)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"deferred edit jobs:", "none running", "build slots (", "queue (this checkout):", "not queued", "mutation run:", "  idle"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q, got:\n%s", want, got)
		}
	}
}

// TestGateStatus_WaitWithNothingRecordedExitsNonZero: --wait on a checkout
// with no deferred job must say so and fail, never hang or fabricate a verdict.
func TestGateStatus_WaitWithNothingRecordedExitsNonZero(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "status", "--wait"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("expected a non-zero exit when there is nothing to wait for, got 0\nstdout: %s", out.String())
	}
	if !strings.Contains(out.String(), "no deferred edit job") {
		t.Errorf("stdout = %q, want it to say why there is nothing to wait for", out.String())
	}
}

// TestGateStatus_WaitFindsTheJobOfTheTreeItIsNamedFromAnotherCwd pins issue
// #732: the harness resets the shell cwd to the primary checkout between
// calls, so a `gate status --wait` resolved from the cwd looked a lane's
// job up under the primary and answered "no deferred edit job recorded"
// while the lane's build was running. The BUILDING line names the tree its
// job belongs to, and --wait given that tree must find the job wherever the
// shell stands.
func TestGateStatus_WaitFindsTheJobOfTheTreeItIsNamedFromAnotherCwd(t *testing.T) {
	gateConfigDir(t)
	lane := t.TempDir()
	tdd.RecordFinishedDeferredJobForTest(lane, "s732")
	inDir(t, t.TempDir())

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "status", "--wait", lane}, strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d, want 0: the job recorded for %s must be found from another cwd\nstdout: %s\nstderr: %s", code, lane, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "gate: deferred") {
		t.Errorf("stdout = %q, want the harvested verdict line", out.String())
	}
}

// TestGateStatus_UnknownFlagIsUsageError: a flag `status` does not have is a
// usage error, not a silent no-op.
func TestGateStatus_UnknownFlagIsUsageError(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "status", "--bogus"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown flag\nstderr: %s", code, errb.String())
	}
}

// TestRun_TopLevelStatus_PrintsTheSameReportAsGateStatus pins issue #435: a
// caller who does not know to type `gate` first must still be able to run
// one command and learn what is running. `aphrollo status` at the top level
// must print exactly what `aphrollo gate status` prints — the same call,
// never a second computation that could drift from it.
func TestRun_TopLevelStatus_PrintsTheSameReportAsGateStatus(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())

	var top, gate bytes.Buffer
	var topErr, gateErr bytes.Buffer
	topCode := Run([]string{"status"}, strings.NewReader(""), &top, &topErr)
	gateCode := Run([]string{"gate", "status"}, strings.NewReader(""), &gate, &gateErr)

	if topCode != gateCode {
		t.Fatalf("exit codes differ: status=%d, gate status=%d", topCode, gateCode)
	}
	if top.String() != gate.String() {
		t.Fatalf("reports differ:\naphrollo status:\n%s\naphrollo gate status:\n%s", top.String(), gate.String())
	}
}
