package tdd

import (
	"errors"
	"os"
	"testing"
)

// TestVerdictFor_BlocksOnTimeoutForEveryRegisteredStage is the table test
// #312 asks for: a new stage cannot silently opt into fail-open just by
// spelling its stage name differently, because verdictFor's timeout mapping
// never branches on the stage string.
func TestVerdictFor_BlocksOnTimeoutForEveryRegisteredStage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	for _, stage := range registeredStages {
		got := verdictFor("precommit", stage, root, "some-cmd", stageOutcome{
			kind:    outcomeTimeout,
			message: "did not finish",
		})
		if !got.Blocked {
			t.Fatalf("stage %q: timeout must block, got unblocked GateResult %+v", stage, got)
		}
	}
}

// TestVerdictFor_BlocksOnCheckErrorForEveryRegisteredStage is the check-error
// half of the same table test: a check whose own machinery could not answer
// (unreadable file, malformed law, a runtime it could not start) blocks for
// every registered stage, not just the ones a past fix happened to touch.
func TestVerdictFor_BlocksOnCheckErrorForEveryRegisteredStage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	for _, stage := range registeredStages {
		got := verdictFor("precommit", stage, root, "some-cmd", stageOutcome{
			kind:    outcomeCheckError,
			err:     errors.New("boom"),
			message: "could not run",
		})
		if !got.Blocked {
			t.Fatalf("stage %q: check-error must block, got unblocked GateResult %+v", stage, got)
		}
	}
}

// TestVerdictFor_PassNeverBlocks pins the other end: a clean run is never
// turned into a rejection by the shared mapping.
func TestVerdictFor_PassNeverBlocks(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	got := verdictFor("precommit", "vet", root, "go vet ./...", stageOutcome{kind: outcomePass})
	if got.Blocked {
		t.Fatalf("a pass must never block, got %+v", got)
	}
}

// TestVerdictFor_TreatsAnOmittedKindAsBlockedNotPass pins #361: a
// stageOutcome literal that never sets kind (a defect in the CALLER, not a
// stage that genuinely passed) used to fall through outcomePass's zero value
// and return an empty, unblocked, unlogged GateResult — a pass nobody
// classified. The zero value must not be a valid pass at all.
func TestVerdictFor_TreatsAnOmittedKindAsBlockedNotPass(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	got := verdictFor("precommit", "vet", root, "some-cmd", stageOutcome{})
	if !got.Blocked {
		t.Fatal("a stageOutcome with no kind set must block, not silently pass")
	}
	requireLoggedVerdict(t, cfg, "unclassified-outcome-rejected")
}

// TestVerdictFor_BlocksAndLogsAnUnrecognizedOutcomeKind is the default
// branch's own defect: a kind outside the switch's named cases (a future
// outcome added to the enum without a case here, e.g. #317's vacuous before
// this change) fell through to the same silent empty pass. The default
// branch must be as loud and blocking as every named case.
func TestVerdictFor_BlocksAndLogsAnUnrecognizedOutcomeKind(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	got := verdictFor("precommit", "vet", root, "some-cmd", stageOutcome{kind: stageOutcomeKind(999)})
	if !got.Blocked {
		t.Fatal("an outcome kind verdictFor cannot classify must block, not silently pass")
	}
	requireLoggedVerdict(t, cfg, "unclassified-outcome-rejected")
}

// TestVerdictFor_BlocksOnVacuousForEveryRegisteredStage is #317's outcome
// slotting into the same shared mapping #361 hardened: zero tests executed
// blocks for every registered stage, with its own log token distinct from
// "blocked" (a real failure) and "timeout-rejected" (never finished) — `gate
// stats` needs to count these separately.
func TestVerdictFor_BlocksOnVacuousForEveryRegisteredStage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	for _, stage := range registeredStages {
		got := verdictFor("precommit", stage, root, "some-cmd", stageOutcome{
			kind:    outcomeVacuous,
			message: "executed zero tests",
		})
		if !got.Blocked {
			t.Fatalf("stage %q: a vacuous run must block, got unblocked GateResult %+v", stage, got)
		}
	}
	requireLoggedVerdict(t, cfg, "vacuous-rejected")
}

// TestGoCheckStage_BlocksOnTimeoutInsteadOfFailingOpen: goCheckStage backs
// `go vet` and golangci-lint. Before this change a TimedOut SuiteResult
// returned an empty, non-blocking GateResult and printed only to stderr — a
// commit whose vet run never finished landed as if vet had never been asked.
func TestGoCheckStage_BlocksOnTimeoutInsteadOfFailingOpen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	run := func(Runner, string) SuiteResult { return SuiteResult{TimedOut: true} }

	got := goCheckStage("precommit", "vet", root, Runner{Cmd: "go", Args: []string{"vet", "./..."}}, run)
	if !got.Blocked {
		t.Fatal("go vet timing out must block the commit, not fail open")
	}
	requireLoggedVerdict(t, cfg, "timeout-rejected")
}

// TestQualityVerdict_BlocksOnTimeoutInsteadOfFailingOpen: qualityVerdict
// backs `cargo fmt --check` and `cargo clippy`. Before this change a
// TimedOut result returned nil (meaning "continue, nothing to report") and
// the commit proceeded with the crate's formatting or lint never actually
// checked.
func TestQualityVerdict_BlocksOnTimeoutInsteadOfFailingOpen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	r := Runner{Cmd: "cargo", Args: []string{"clippy", "-p", "a"}}

	got := qualityVerdict("precommit", root, "a", "clippy", r, SuiteResult{TimedOut: true}, true)
	if got == nil || !got.Blocked {
		t.Fatalf("clippy timing out must block the commit, got %+v", got)
	}
	requireLoggedVerdict(t, cfg, "timeout-rejected")
}

// TestRatchetFixtureStage_BlocksWhenRunFixturesErrors: a law file this
// binary's schema cannot even parse used to print "ratchet fixtures →
// skipped" and let the commit through — the same shape #158 fixed for
// ratchet check, never applied to the fixture stage.
func TestRatchetFixtureStage_BlocksWhenRunFixturesErrors(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	lawsDir := root + "/.ratchet/laws"
	if err := os.MkdirAll(lawsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lawsDir+"/bad.toml", []byte("this is not === valid toml {{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ratchetFixtureStage("precommit", root)
	if !got.Blocked {
		t.Fatal("a law RunFixtures cannot even parse must block the commit, not skip it")
	}
	requireLoggedVerdict(t, cfg, "check-error-rejected")
}

// TestDocsCheckStage_BlocksWhenCheckFilesErrors: docsCheckStage's own doc
// comment describes a zero-bar check, but a markdown file staged in the
// index and then removed from disk used to print "docs → skipped" and let
// the commit through — a suppression built from making the file unreadable.
func TestDocsCheckStage_BlocksWhenCheckFilesErrors(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "note.md", "# note\n")
	gitDo(t, root, "add", "note.md")
	if err := os.Remove(root + "/note.md"); err != nil {
		t.Fatal(err)
	}

	got := docsCheckStage("precommit", root)
	if !got.Blocked {
		t.Fatal("docs.CheckFiles erroring on an unreadable staged file must block, not skip")
	}
	requireLoggedVerdict(t, cfg, "check-error-rejected")
}
