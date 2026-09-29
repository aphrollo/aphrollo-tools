package precommit

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// Issue #921, from borld: `server` is on the clippy-clean list and depends on
// `forge_solver`, which is not. The commit gate's `-D warnings` run over
// server also linted forge_solver, so three warnings already on main refused
// a commit that touched only server. They reached main because the merge gate
// never ran the clippy-clean stage for the listed crates downstream of what a
// merge touched. These run real cargo over a two-crate workspace of the same
// shape.

// clippyCleanServerWorkspace commits a workspace where `server` (listed
// clippy-clean) depends on `solver` (not listed), which carries two clippy
// warnings, and returns the repo root with nothing staged.
func clippyCleanServerWorkspace(t *testing.T) string {
	t.Helper()
	tddtest.RequireRealCargo(t)
	linterAbsent(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"server\", \"solver\"]\nresolver = \"2\"\n\n"+
		"[workspace.metadata.aphrollo]\nclippy-clean = [\"server\"]\n")
	write(t, root, ".gitignore", "target/\n")
	write(t, root, "solver/Cargo.toml", "[package]\nname = \"solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "solver/src/lib.rs", solverWithWarnings(""))
	write(t, root, "server/Cargo.toml", "[package]\nname = \"server\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[dependencies]\nsolver = { path = \"../solver\" }\n")
	write(t, root, "server/src/lib.rs", "pub fn serve() -> i32 {\n    solver::clampish(3)\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// solverWithWarnings is solver's source: a clamp-like pattern and an
// immediately dereferenced reference, both clippy warnings, with attr placed
// on clampish (a `#[deprecated]` there makes every caller warn).
func solverWithWarnings(attr string) string {
	return attr + "pub fn clampish(x: i32) -> i32 {\n    if x < 0 {\n        0\n    } else if x > 10 {\n        10\n    } else {\n        x\n    }\n}\n\n" +
		"pub fn reborrow(v: &i32) -> i32 {\n    *&*v\n}\n"
}

// Serial: sets the process-wide env var CARGO_HOME.
func TestPrecommit_ClippyClean_AdmitsACommitWhoseOnlyWarningsLiveInAnUnlistedDependency(t *testing.T) {
	root := clippyCleanServerWorkspace(t)
	write(t, root, "server/src/lib.rs", "pub fn serve() -> i32 {\n    solver::clampish(4)\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))

	if res.Blocked {
		t.Fatalf("the warnings live in solver, which the commit did not touch and which is not clippy-clean; got blocked: %s", res.Message)
	}
}

// Serial: sets the process-wide env var CARGO_HOME.
func TestPrecommit_ClippyClean_RefusesAWarningInTheListedCrateItself(t *testing.T) {
	root := clippyCleanServerWorkspace(t)
	write(t, root, "server/src/lib.rs", "pub fn serve() -> i32 {\n    solver::clampish(4)\n}\n\n"+
		"pub fn peek(v: &i32) -> i32 {\n    *&*v\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))

	if !res.Blocked {
		t.Fatal("a new warning in server, which is clippy-clean, must refuse the commit")
	}
	if !strings.Contains(res.Message, "crate server") || !strings.Contains(res.Message, "immediately dereferencing") {
		t.Fatalf("the refusal must name server and the warning, got: %s", res.Message)
	}
}

// The merge touches only solver, yet its change makes server warn: a
// deprecated clampish turns server's call into a warning. server is
// clippy-clean and downstream of solver, so the merge must refuse it.
// Serial: sets the process-wide env var CARGO_HOME.
func TestMechanical_ClippyClean_RefusesAWarningInAListedCrateDownstreamOfTheMerge(t *testing.T) {
	root := clippyCleanServerWorkspace(t)
	write(t, root, "solver/src/lib.rs", solverWithWarnings("#[deprecated(note = \"use clamp\")]\n"))
	gitDo(t, root, "add", ".")

	res := Mechanical(root, RunSuite(precommitTestTimeout))

	if !res.Blocked {
		t.Fatal("the merge makes server, which is clippy-clean, warn; the merge must be refused")
	}
	if !strings.Contains(res.Message, "crate server") || !strings.Contains(res.Message, "deprecated") {
		t.Fatalf("the refusal must name server and the deprecation warning, got: %s", res.Message)
	}
}
