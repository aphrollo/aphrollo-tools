package tdd

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// gateEnvJobName is the pipeline.yml job this file pins to tempEnvKeys
// (gotmpdir_test.go). pipeline.yml's gate-env job HARDCODES the same four
// variable names in YAML, rather than deriving them from goTmpEnv
// (gotmpdir.go) at YAML-eval time, because goTmpEnv is unexported and
// nothing short of a build-time entry point added solely to serve one
// workflow step would let a workflow read it directly. A hardcoded second
// copy of the set a first-party function returns is worth nothing if it can
// silently drift from that function -- this file is the check that closes
// that gap.
const gateEnvJobName = "gate-env"

// jobKeyLine matches a top-level job key: two-space indent, no further
// indentation, a bare "name:" with nothing after it on the line.
var jobKeyLineRe = regexp.MustCompile(`^  [A-Za-z][A-Za-z0-9_-]*:\s*$`)

// exportAssignmentRe reads the LHS of every shell `NAME=value` assignment on
// one line.
var exportAssignmentRe = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)=`)

// TestGateEnvWorkflowStep_ExportsExactlyTempEnvKeys fails in EITHER
// direction a hardcoded second copy can drift from the function it
// duplicates: add a fifth name to tempEnvKeys with pipeline.yml left alone,
// or delete one of the four `export` lines from pipeline.yml's gate-env job
// with tempEnvKeys left alone, and the two sides stop matching. A test that
// only checked the YAML MENTIONS every key in tempEnvKeys would pass forever
// once written and would never see a key ADDED to tempEnvKeys -- exactly the
// direction of drift that leaves the job green while it silently stops
// testing the environment the gate actually builds.
func TestGateEnvWorkflowStep_ExportsExactlyTempEnvKeys(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")

	job := gateEnvJobBlock(t, wf)
	got := exportedVarNames(job)
	if len(got) == 0 {
		t.Fatalf("no `export NAME=...` line found inside the %q job, so this test proves nothing", gateEnvJobName)
	}

	want := append([]string(nil), tempEnvKeys...)
	sort.Strings(want)
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pipeline.yml's %q job exports %v, want exactly tempEnvKeys %v (goTmpEnv's own set, gotmpdir.go) -- not a subset, not a superset", gateEnvJobName, got, want)
	}
}

// gateEnvJobBlock isolates one top-level job's own YAML text: from its
// "  <name>:" key to the next top-level job key (two-space indent) or EOF,
// mirroring pipeline_push_test.go's own escape-closure isolation but without
// hardcoding the NEXT job's name, so reordering the file cannot silently
// widen this test's scope to a neighboring job's export lines.
func gateEnvJobBlock(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+gateEnvJobName+":" {
				start = i
			}
			continue
		}
		if jobKeyLineRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("no %q job in pipeline.yml, so this test proves nothing", gateEnvJobName)
	}
	return strings.Join(lines[start:], "\n")
}

// exportedVarNames returns every distinct variable name assigned on an
// `export ...` line within block. Scoped to lines whose TRIMMED text starts
// with "export " so a variable merely referenced as a VALUE (`$GOTMPDIR`
// inside another assignment's RHS) is never mistaken for one being SET.
func exportedVarNames(block string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "export ") {
			continue
		}
		for _, m := range exportAssignmentRe.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
