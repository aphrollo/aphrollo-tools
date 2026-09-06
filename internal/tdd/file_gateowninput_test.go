package tdd

import "testing"

// tools/mutation_gate.sh is not prose: mutants_runner_name.go,
// mutants_run.go and mutants_producer_version.go all
// fileExists(filepath.Join(root, "tools", "mutation_gate.sh")) to pick the
// mutation producer, so a change to it changes what the gate DOES. Left
// Ignore (no .sh extension in sourceExts, and _test.sh matches no test
// convention either), a commit editing only this script -- or only its
// paired tools/mutation_gate_test.sh -- took the docs-only fast path: tree
// guards ran, no suite, no build lock, and the change went untested by
// anything but a human running the 10 cases by hand (issue #469).
func TestClassifyFile_TreatsTheMutationRunnerScriptAsSourceSoItCannotTakeTheDocsOnlyPath(t *testing.T) {
	if got := ClassifyFile("tools/mutation_gate.sh"); got != Source {
		t.Errorf("ClassifyFile(%q) = %v, want %v — the gate's own producer script is judged by hand only when the tree guards at least ran", "tools/mutation_gate.sh", got, Source)
	}
}

// An UNRELATED shell script -- even one that is also tooling nothing else
// pins -- must stay Ignore: tools/clippy_clean_list.sh is executable too, but
// it is only ever MENTIONED inside a diagnostic string in doctor.go, never
// opened, read or executed by that literal path, so it carries none of the
// weight that makes mutation_gate.sh load-bearing.
func TestClassifyFile_DoesNotTreatAnArbitraryToolingScriptAsSource(t *testing.T) {
	if got := ClassifyFile("tools/clippy_clean_list.sh"); got != Ignore {
		t.Errorf("ClassifyFile(%q) = %v, want %v — being executable tooling is not the discriminator; being read or run BY NAME from this tool's own source is", "tools/clippy_clean_list.sh", got, Ignore)
	}
}

// TestDocsOnly_IsFalseWhenTheMutationRunnerScriptIsStaged is the consequence
// asserted where it actually bites, mirroring
// TestDocsOnly_IsFalseWhenTheGateConfigIsStaged for the producer script.
func TestDocsOnly_IsFalseWhenTheMutationRunnerScriptIsStaged(t *testing.T) {
	tests, srcs := splitKinds([]string{"README.md", "tools/mutation_gate.sh"})
	if len(tests) != 0 {
		t.Errorf("tests = %q, want none", tests)
	}
	if len(srcs) != 1 || srcs[0] != "tools/mutation_gate.sh" {
		t.Errorf("srcs = %q, want [tools/mutation_gate.sh] — otherwise docsOnly reports true and the tree guards are the only thing that ran", srcs)
	}
}

// .github/workflows/pipeline.yml is not prose either: pipeline_push_test.go
// reads this repo's own checked-in copy at that literal path and asserts on
// its contents, and mutants_ci_test.go/mutants_ci_pipeline_test.go and
// precommit_go_test.go assert on synthesized copies of it. Left Ignore (no
// sourceExts entry for .yml), a commit editing only pipeline.yml mapped to NO
// Go package at all -- not even the module root -- so the mechanical stage
// had nothing to scope to and never ran internal/tdd, the package whose own
// tests read it (issue #444).
func TestClassifyFile_TreatsThePipelineWorkflowAsSourceSoItCannotTakeTheDocsOnlyPath(t *testing.T) {
	if got := ClassifyFile(".github/workflows/pipeline.yml"); got != Source {
		t.Errorf("ClassifyFile(%q) = %v, want %v — this repo's own CI definition is judged by the suite that reads it", ".github/workflows/pipeline.yml", got, Source)
	}
}

// TestDocsOnly_IsFalseWhenThePipelineWorkflowIsStaged is the consequence
// asserted where it actually bites.
func TestDocsOnly_IsFalseWhenThePipelineWorkflowIsStaged(t *testing.T) {
	tests, srcs := splitKinds([]string{"README.md", ".github/workflows/pipeline.yml"})
	if len(tests) != 0 {
		t.Errorf("tests = %q, want none", tests)
	}
	if len(srcs) != 1 || srcs[0] != ".github/workflows/pipeline.yml" {
		t.Errorf("srcs = %q, want [.github/workflows/pipeline.yml] — otherwise docsOnly reports true and the suite that pins it never runs", srcs)
	}
}
