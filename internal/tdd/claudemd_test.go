package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shimDir = "C:/Users/olive/bin/cargo-queue"

func TestClaudeMDBlockCarriesTheOperatingInstructions(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	for _, want := range []string{
		claudeMDBegin, claudeMDEnd, shimDir,
		"gate:", "QUEUED-SKIPPED", "cargo check -p", ".ratchet/laws",
		"aphrollo ratchet", "aphrollo gate gc", "aphrollo gate stats",
		"aphrollo install",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not mention %q", want)
		}
	}
	// The receipt is deleted: the run measures and refuses, and nothing signs,
	// carries or checks a document afterwards. A block still promising one
	// teaches every repo that gets it to wait for a proof that is never
	// written.
	if strings.Contains(strings.ToLower(block), "receipt") {
		t.Error("the block still promises a mutation receipt — nothing produces one")
	}
	if n := strings.Count(block, "\n"); n < 15 || n > 40 {
		t.Errorf("block is %d lines — it has to be readable in one glance", n)
	}
	if strings.Contains(block, "commit-msg") {
		t.Error("a workspace that did not ask for the undercover rule must not be told it")
	}
	if !strings.Contains(ClaudeMDBlock(shimDir, true), "commit-msg") {
		t.Error("a workspace with undercover = true must get the commit-message rule")
	}
}

// TestClaudeMDBlock_VetLintClaimMatchesGoOnlyGuard is a cheap trip-wire for
// #548's cold-review yellow: the block claims a Go root runs vet/lint,
// which is true only because precommit_gateroot.go's goQualityStage is
// guarded by `runner.Cmd == "go"` — pytest/vitest/zig roots get neither.
// This does not verify the prose is accurate in general (no general
// truth-checker over English is being attempted here) — only that the ONE
// condition this specific claim depends on has not silently moved
// underneath it, which is the same block-versus-binary drift #518 exists to
// close, caught here instead of by a human re-reading both by hand.
func TestClaudeMDBlock_VetLintClaimMatchesGoOnlyGuard(t *testing.T) {
	src, err := os.ReadFile("precommit_gateroot.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `if runner.Cmd == "go" {`) {
		t.Fatal(`precommit_gateroot.go no longer guards goQualityStage with runner.Cmd == "go" — update the "a Go root also runs vet/lint" claim in ClaudeMDBlock's stage-list bullet to match`)
	}
	block := ClaudeMDBlock(shimDir, false)
	if !strings.Contains(block, "a Go root also runs vet/lint") {
		t.Fatal(`ClaudeMDBlock no longer scopes its vet/lint claim to "a Go root" — check it still matches precommit_gateroot.go's runner.Cmd == "go" guard before broadening it`)
	}
}

// The block described the mutation run only as something post-commit spawns,
// so every repo that gets it learned the gate's mutation half without ever
// learning the command a person types. Four sessions independently reached for
// the repo's own producer script instead — which runs outside the box-wide
// lock and in the wrong tree — and one traced the habit to a skill that
// spelled the script out as a numbered step. There is no spawned form left,
// so the block names the typed one and nothing else: a session told the
// commit starts a run waits for one that never starts.
// The block listed the commit gate as ending in "suites", which the gate
// stopped doing when the mechanical suite moved to the merge. A session then
// read a correct precommit — fail-first and no suites line — as a suite that
// had failed to run, applied the rule that a missing verdict means untested,
// and spent a full package run proving what the gate had already decided not
// to ask for. The block is the instruction sessions act on, so a claim it
// makes about a stage that no longer exists costs a suite every commit.
func TestClaudeMDBlock_DoesNotPromiseASuiteTheCommitGateNoLongerRuns(t *testing.T) {
	src, err := os.ReadFile("precommit_gateroot.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "return failFirstStage(repoRoot, g.root, g.tests, g.srcs, run)") {
		t.Fatal("precommit_gateroot.go no longer ends its fail-first branch at failFirstStage — if the " +
			"commit gate runs a suite again, restore the claim in ClaudeMDBlock's stage-list bullet")
	}
	block := ClaudeMDBlock(shimDir, false)
	if strings.Contains(block, "fail-first→\n  suites") || strings.Contains(block, "fail-first→suites") {
		t.Error("the block still says the commit gate ends in suites, but the fail-first branch returns " +
			"at failFirstStage — a session that believes it re-runs the package the gate deliberately skipped")
	}
	if !strings.Contains(block, "merge") {
		t.Error("the block does not say where the mechanical suite DID go — a session told only that the " +
			"commit gate skips it cannot tell a skipped suite from a missing one")
	}
}

func TestClaudeMDBlock_NamesTheHandTypedMutationRunNotOnlyTheSpawnedOne(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	if !strings.Contains(block, "aphrollo gate mutants run") {
		t.Error("the block never names the command that measures the current lane by hand")
	}
	if strings.Contains(block, "postcommit") {
		t.Error("the block still hands the mutation run to the commit — post-commit starts nothing")
	}
}

// A rule the gate enforces but never states reads to a session as an
// arbitrary refusal, so the merge-only rule and its recipe ride in the block
// every repo gets.
func TestClaudeMDBlockStatesThePrimaryCheckoutRule(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	for _, want := range []string{
		"primary checkout",
		"merge-only",
		"git worktree add -b lane/<name>",
		".worktrees/<repo>/<name>",
		"aphrollo gate allow primary",
		"aphrollo gate revoke primary",
		// The Bash/PowerShell hooks classify a command before it runs and can
		// miss; the git shim judges the actual command and is what a session
		// must not mistake the hook for (issue #118).
		"GUARDRAIL",
		"WALL",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not state %q", want)
		}
	}
}

func TestPatchClaudeMDAppendsOnceAndIsIdempotent(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	first, changed := PatchClaudeMD([]byte("# Project\n\nSome guidance.\n"), block)
	if !changed {
		t.Fatal("a file with no block must gain one")
	}
	if !strings.HasSuffix(string(first), block) {
		t.Errorf("the block must land at the END:\n%s", first)
	}

	second, changed := PatchClaudeMD(first, block)
	if changed {
		t.Error("a second run must change nothing")
	}
	if string(second) != string(first) {
		t.Error("a second run must be byte-identical")
	}
	if strings.Count(string(second), claudeMDBegin) != 1 {
		t.Errorf("the block was duplicated:\n%s", second)
	}
}

// A block already present is replaced where it sits: someone may have put it
// somewhere deliberate, and moving it to the end on every init would churn the
// file forever.
func TestPatchClaudeMDReplacesAnExistingBlockInPlace(t *testing.T) {
	t.Parallel()
	stale := "# Project\n\n" + claudeMDBegin + "\nold text nobody updated\n" + claudeMDEnd + "\n\n## Conventions\n\nkeep me\n"
	block := ClaudeMDBlock(shimDir, false)

	out, changed := PatchClaudeMD([]byte(stale), block)
	got := string(out)
	if !changed {
		t.Fatal("a stale block must be replaced")
	}
	if strings.Contains(got, "old text nobody updated") {
		t.Errorf("the stale block survived:\n%s", got)
	}
	if strings.Count(got, claudeMDBegin) != 1 || strings.Count(got, claudeMDEnd) != 1 {
		t.Errorf("markers were duplicated:\n%s", got)
	}
	if !strings.Contains(got, "## Conventions") || !strings.Contains(got, "keep me") {
		t.Errorf("content after the block was lost:\n%s", got)
	}
	if strings.Index(got, claudeMDBegin) > strings.Index(got, "## Conventions") {
		t.Errorf("the block moved instead of being replaced in place:\n%s", got)
	}
}

// A file hand-edited mid-block leaves one marker behind. Nesting a fresh block
// inside a half-open one would make every later init unparseable.
func TestPatchClaudeMDRecoversFromAnOrphanMarker(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	out, _ := PatchClaudeMD([]byte("# Project\n\n"+claudeMDBegin+"\nhalf a block\n"), block)
	got := string(out)
	if strings.Count(got, claudeMDBegin) != 1 || strings.Count(got, claudeMDEnd) != 1 {
		t.Fatalf("markers are not balanced:\n%s", got)
	}
	if !strings.HasSuffix(got, block) {
		t.Errorf("the recovered file must end with one whole block:\n%s", got)
	}
}

// The file's last byte can be the end marker itself, with no trailing
// newline at all — a CLAUDE.md someone truncated exactly there, or one whose
// editor strips a final newline. The replace-in-place branch must not assume
// a byte survives past the marker to skip. Found by issue #286.
func TestPatchClaudeMD_ReplacesInPlaceWhenFileEndsExactlyAtTheEndMarkerWithNoTrailingNewline(t *testing.T) {
	t.Parallel()
	stale := "# Project\n\n" + claudeMDBegin + "\nold text\n" + claudeMDEnd
	block := ClaudeMDBlock(shimDir, false)

	out, changed := PatchClaudeMD([]byte(stale), block)
	got := string(out)
	if !changed {
		t.Fatal("a stale block must be replaced")
	}
	if strings.Contains(got, "old text") {
		t.Errorf("the stale block survived:\n%s", got)
	}
	if strings.Count(got, claudeMDBegin) != 1 || strings.Count(got, claudeMDEnd) != 1 {
		t.Errorf("markers were duplicated:\n%s", got)
	}
	if !strings.HasPrefix(got, "# Project") {
		t.Errorf("content before the block was lost:\n%s", got)
	}
}

func TestPatchClaudeMDPreservesCRLF(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(shimDir, false)
	out, _ := PatchClaudeMD([]byte("# Project\r\n\r\nGuidance.\r\n"), block)
	if strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Error("a CRLF file must stay CRLF throughout")
	}
	again, changed := PatchClaudeMD(out, block)
	if changed || string(again) != string(out) {
		t.Error("a CRLF file must be byte-identical on a second run")
	}
}

func TestWriteClaudeMDIsANoOpWithoutTheFileUnlessForced(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	changed, err := WriteClaudeMD(repo, shimDir, false)
	if err != nil || changed {
		t.Fatalf("a repo with no CLAUDE.md must be left alone (changed=%v err=%v)", changed, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("no file must be invented")
	}

	changed, err = WriteClaudeMD(repo, shimDir, true)
	if err != nil || !changed {
		t.Fatalf("--claude-md must create the file (changed=%v err=%v)", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if err != nil || !strings.HasPrefix(string(data), claudeMDBegin) {
		t.Fatalf("created file = %q (%v)", data, err)
	}
}

// Run twice, byte-identical: the file is a source file in the consuming repo,
// and a block that churned would show up as a diff on every session start.
func TestWriteClaudeMDIsByteIdenticalOnASecondRun(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	path := filepath.Join(repo, "CLAUDE.md")
	mustWrite(t, path, "# Borld\n\nProject guide.\n")
	mustWrite(t, filepath.Join(repo, "Cargo.toml"),
		"[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\n")

	if changed, err := WriteClaudeMD(repo, shimDir, false); err != nil || !changed {
		t.Fatalf("first run: changed=%v err=%v", changed, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "commit-msg") {
		t.Error("the workspace's undercover flag must reach the block")
	}

	if changed, err := WriteClaudeMD(repo, shimDir, false); err != nil || changed {
		t.Fatalf("second run: changed=%v err=%v — nothing moved, so nothing should be written", changed, err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Error("the second run rewrote the file")
	}
	if !strings.Contains(string(second), "Project guide.") {
		t.Error("the repo's own guidance was lost")
	}
}
