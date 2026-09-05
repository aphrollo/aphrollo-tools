package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestGateStatus_NoDeferredNoBuildNoMutants_StillPrintsAllThreeSections is
// the plain, nothing-going-on case for `aphrollo gate status` (issue #430):
// it must still print all three sections, each saying explicitly that it
// found nothing, rather than a silent or partial report.
func TestGateStatus_NoDeferredNoBuildNoMutants_StillPrintsAllThreeSections(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir()) // outside a git repo: the mutation section reports an error, not a hang

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "status"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (status is read-only and never fails the session)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"deferred edit jobs:", "none running", "build slots (", "mutation run"} {
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
