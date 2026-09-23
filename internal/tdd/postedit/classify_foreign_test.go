package postedit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func postEditVerdicts(t *testing.T, cfg string) []string {
	t.Helper()
	return tddtest.PostEditVerdicts(t, cfg, func(line string) (string, string, bool) { e, ok := parseGateLine(line); return e.Stage, e.Verdict, ok })
}

// A link step that fails on a crate this edit never touched says nothing
// about the edit: a stale artifact, a half-written sibling crate or another
// session's concurrent build produces `rust-lld: error: undefined symbol` and
// `could not compile <other crate>` no matter what this session wrote. It was
// classified RED, sending the session to fix code it had not changed
// (issue #593). It belongs with TIMEOUT and the spawn/slot failures: the code
// was not tested.
func TestPostEditFile_CountsALinkFailureInAnUntouchedCrateAsInfraNotRed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, filepath.Join("src", "lib.rs"), "pub fn base() -> i32 { 1 }\n")

	failed := SuiteResult{Passed: false, Output: "" +
		"rust-lld: error: undefined symbol: server::headless_server_app_with\n" +
		"error: could not compile `server` (lib test) due to 1 previous error\n"}
	run := func(Runner, string) SuiteResult { return failed }

	text, _ := postEditFile("s593c", filepath.Join(root, "src", "lib.rs"), run)

	want := []string{InfraFailed}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
	}
	if !strings.Contains(text, "server") {
		t.Fatalf("the advisory must name the crate that failed to link, got %q", text)
	}
}

// A heavy cargo build outruns the edit budget, so its result is harvested by
// a LATER hook — which is where the reported link failure actually landed. A
// deferred phase carries the file its edit was about for exactly this reason:
// without it the harvest cannot tell whose crate failed, and classified the
// same foreign link failure as RED.
func TestEditResultAdvisory_CountsALinkFailureInAnUntouchedCrateAsInfraNotRed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	log := filepath.Join(t.TempDir(), "phase.log")
	if err := os.WriteFile(log, []byte("rust-lld: error: undefined symbol: server::headless_server_app_with\n"+
		"error: could not compile `server` (lib test) due to 1 previous error\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := DeferredJob{
		Project: root, Phase: "build", Dir: root, Log: log,
		Runner: []string{"cargo", "nextest", "run"},
		File:   filepath.Join(root, "src", "lib.rs"),
	}

	text := editResultAdvisory(j, PhaseOutcome{ExitCode: 101}, root, nil, "", "")

	want := []string{InfraFailed}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this harvest, want %v", got, want)
	}
	if !strings.Contains(text, "server") {
		t.Fatalf("the advisory must name the crate that failed to link, got %q", text)
	}
}

// The same output for the crate the edit DID touch is an ordinary failure:
// deleting a symbol its own bench or test still references breaks the link,
// and that is this session's to fix. The infra class must not swallow it.
func TestPostEditFile_KeepsALinkFailureInTheEditedCrateRed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, filepath.Join("src", "lib.rs"), "pub fn base() -> i32 { 1 }\n")

	failed := SuiteResult{Passed: false, Output: "" +
		"rust-lld: error: undefined symbol: m::gone\n" +
		"error: could not compile `m` (lib test) due to 1 previous error\n"}
	run := func(Runner, string) SuiteResult { return failed }

	postEditFile("s593d", filepath.Join(root, "src", "lib.rs"), run)

	want := []string{string(Red)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v for this run, want %v", got, want)
	}
}
