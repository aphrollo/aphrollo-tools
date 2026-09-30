package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// mutants-before-pr = "ci" hands the measurement to CI's mutants-verdict
// check: the verbs that open a PR neither measure nor need --skip-mutants,
// and say so in one line.
func TestPR_CIModeSkipsTheLocalMeasurementAndSaysSo(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, false)
	writeRel(t, repo, "Cargo.toml",
		"[workspace]\nmembers = [\"crates/a\"]\n\n[workspace.metadata.aphrollo]\nmutants-before-pr = \"ci\"\n")
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}

	if fake.calls != 0 {
		t.Errorf("a repo that measures in CI was measured locally %d time(s)", fake.calls)
	}
	if !strings.Contains(out.String(), "mutants: measured in CI (mutants-verdict)\n") {
		t.Errorf("stdout = %q, want the one line saying the measurement is CI's", out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
}
