package precommit

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// The commit gate runs the commit-time mutation stage after its roots have
// passed, when the repo declares `mutants-at-commit`, and a survivor of a
// line the commit adds refuses the commit. The stage's own behaviour is
// proved in the mutation package; what is proved here is that the gate calls
// it, when, and that its refusal is the gate's.

const widgetKind = "package m\n\nfunc Kind(n int) string {\n\tif n > 10 {\n\t\treturn \"big\"\n\t}\n\treturn \"small\"\n}\n"

func commitMutationRepo(t *testing.T, declare bool) (cfgDir, root string) {
	t.Helper()
	cfgDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "" }))
	linterAbsent(t)
	root = makeGoRepo(t)
	if declare {
		write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = \"block\"\n")
	}
	return cfgDir, root
}

// Serial: installs a process-wide test override (SetCommitExecForTest).
func TestPrecommit_ASurvivingMutantOfAnAddedLineRefusesTheCommit(t *testing.T) {
	_, root := commitMutationRepo(t, true)
	t.Cleanup(SetCommitExecForTest(func(context.Context, string, []string, []string, io.Writer) (int, error) {
		return 0, nil
	}))
	write(t, root, "widget.go", widgetKind)
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, runsAt(&seen, root))

	if !res.Blocked {
		t.Fatalf("a commit with a surviving mutant was let through: %q", res.Message)
	}
	if !strings.Contains(res.Message, "widget.go:4:7: CONDITIONALS_BOUNDARY") {
		t.Errorf("message = %q, want the surviving mutant named", res.Message)
	}
}

// Serial: installs a process-wide test override (SetCommitExecForTest).
func TestPrecommit_MutantsTheTestsKillPassTheCommit(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfgDir, root := commitMutationRepo(t, true)
	t.Cleanup(SetCommitExecForTest(func(_ context.Context, _ string, _ []string, argv []string, log io.Writer) (int, error) {
		for _, a := range argv {
			if a == "-overlay" {
				_, _ = io.WriteString(log, "FAIL\tm\n")
				return 1, nil
			}
		}
		return 0, nil
	}))
	write(t, root, "widget.go", widgetKind)
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("a commit whose mutants were all caught was refused: %s", res.Message)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-passed:tested=2,caught=2") {
		t.Errorf("gate.log = %q, want the measurement counted", log)
	}
}

// A repo that never declared the key is not measured and not told anything.
// Serial: installs a process-wide test override (SetCommitExecForTest).
func TestPrecommit_TheCommitMutationStageIsInertWhenUndeclared(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfgDir, root := commitMutationRepo(t, false)
	t.Cleanup(SetCommitExecForTest(func(context.Context, string, []string, []string, io.Writer) (int, error) {
		t.Error("a mutant was run in a repo that declares no mutants-at-commit")
		return 0, nil
	}))
	write(t, root, "widget.go", widgetKind)
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if log := gateLogText(t, cfgDir); strings.Contains(log, "mutants-") {
		t.Errorf("gate.log mentions mutants in an undeclared repo:\n%s", log)
	}
}

// The stage runs after the root's own checks: a commit those refuse never
// pays for a mutation run.
// Serial: installs a process-wide test override (SetCommitExecForTest).
func TestPrecommit_ARefusedRootNeverReachesTheMutationStage(t *testing.T) {
	_, root := commitMutationRepo(t, true)
	t.Cleanup(SetCommitExecForTest(func(context.Context, string, []string, []string, io.Writer) (int, error) {
		t.Error("a mutant was run for a commit the vet stage refused")
		return 0, nil
	}))
	write(t, root, "widget.go", widgetKind)
	gitDo(t, root, "add", ".")

	failing := func(r Runner, dir string) SuiteResult {
		return SuiteResult{Passed: false, Output: "vet: broken"}
	}
	if res := Precommit(root, failing); !res.Blocked {
		t.Fatal("a failing vet did not refuse the commit")
	}
}
