package precommit

import (
	"strings"
	"testing"
)

// Issue #739, borld escape 6aafbdfa: a lane changed forge and five other
// crates, and the merge gate's check stage printed "check scope → ... forge
// forge_lab ... (touched crates + everything downstream of them)" — then ran
// the mechanical suite for the touched crates only. forge_lab, untouched and
// downstream of forge, pins forge's trajectory bit for bit; the pin moved,
// and main went red until the NEXT lane's merge gate happened to run it.
//
// A change can break a crate downstream of it as surely as it can break
// itself, and the check stage already computes exactly that set. The suite
// the merge runs has to cover the same ground the check stage names.

// downstreamWorkspace is a three-crate cargo workspace: lab depends on core
// through a path dependency, aside depends on nothing. A source change to
// core is staged. The graph the gate reads is stated to match the manifests:
// this package's suite runs without a Rust toolchain, and cargo's own
// reading of a graph is clippyscope_test.go's business.
func downstreamWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/core\", \"crates/lab\", \"crates/aside\"]\nresolver = \"2\"\n")
	write(t, root, "crates/core/Cargo.toml", "[package]\nname = \"core_sim\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "crates/core/src/lib.rs", "pub fn step() -> f64 { 1.0 }\n")
	write(t, root, "crates/lab/Cargo.toml", "[package]\nname = \"lab\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[dependencies]\ncore_sim = { path = \"../core\" }\n")
	write(t, root, "crates/lab/src/lib.rs", "pub fn pin() -> u64 { core_sim::step().to_bits() }\n")
	write(t, root, "crates/aside/Cargo.toml", "[package]\nname = \"aside\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "crates/aside/src/lib.rs", "pub fn other() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "crates/core/src/lib.rs", "pub fn step() -> f64 { 1.0 + f64::EPSILON }\n")
	gitDo(t, root, "add", ".")
	return root
}

// suiteRuns records every suite command a gate ran, green throughout.
func suiteRuns(ran *[]string) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		if isSuiteVerb(r) {
			*ran = append(*ran, strings.Join(r.Args, " "))
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed\n"}
	}
}

func TestMechanical_RunsTheSuiteOfACrateDownstreamOfATouchedOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})

	var ran []string
	if res := Mechanical(root, suiteRuns(&ran)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	joined := strings.Join(ran, " | ")
	if !strings.Contains(joined, "-p core_sim") {
		t.Fatalf("premise broken — the merge gate must run the touched crate's suite; ran %v", ran)
	}
	if !strings.Contains(joined, "-p lab") {
		t.Fatalf("lab depends on the touched crate and pins its output, yet the merge gate never ran its suite; ran %v", ran)
	}
	if strings.Contains(joined, "-p aside") || strings.Contains(joined, "--workspace") {
		t.Fatalf("aside is not downstream of anything that moved, and the merge gate must not widen to the whole workspace; ran %v", ran)
	}
}

// Nextest never runs a doctest, so a crate's doctests are a suite of their
// own at the merge — and a downstream crate's doctest calls the moved code
// exactly as its tests do.
func TestMechanical_RunsTheDoctestsOfACrateDownstreamOfATouchedOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	write(t, root, "crates/lab/src/lib.rs", "/// ```\n/// assert_eq!(lab::pin(), 1.0f64.to_bits());\n/// ```\n"+
		"pub fn pin() -> u64 { core_sim::step().to_bits() }\n")
	gitDo(t, root, "commit", "-qm", "lab doctest", "--", "crates/lab/src/lib.rs")

	var doctests []string
	res := Mechanical(root, func(r Runner, _ string) SuiteResult {
		if args := strings.Join(r.Args, " "); strings.HasSuffix(args, "--doc") {
			doctests = append(doctests, args)
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed\n"}
	})
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if strings.Join(doctests, " | ") != "test -p lab --doc" {
		t.Fatalf("doctests run = %v, want exactly lab's: it is downstream of the touched crate and the only crate here carrying a doctest", doctests)
	}
}

// The commit gate runs no suite, and names what the merge will run instead.
// That list is the merge's own scope, downstream crates included, or the
// commit's output understates what is still owed.
func TestPrecommit_NamesADownstreamCrateAmongTheSuitesItDidNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})

	var ran []string
	out := captureStderr(t, func() {
		if res := Precommit(root, suiteRuns(&ran)); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	})
	var notRun string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "NOT RUN") {
			notRun = line
		}
	}
	if !strings.Contains(notRun, "core_sim, lab not tested here") {
		t.Fatalf("NOT RUN line = %q, want it to name both the touched crate and lab downstream of it; output:\n%s", notRun, out)
	}
}
