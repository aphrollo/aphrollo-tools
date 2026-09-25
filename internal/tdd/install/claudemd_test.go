package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeMDBlockCarriesTheOperatingInstructions(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{})
	for _, want := range []string{
		claudeMDBegin, claudeMDEnd, "cargo-queue",
		"gate:", "QUEUED-SKIPPED", "cargo check -p", ".ratchet/laws",
		"aphrollo ratchet", "aphrollo gate gc", "aphrollo gate stats",
		"aphrollo install",
		// A deferred verdict is waited on with the tree the BUILDING line
		// names, never the shell cwd the harness resets (issue #732).
		"aphrollo gate status --wait <tree>",
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
	if !strings.Contains(ClaudeMDBlock(BlockFlags{Undercover: true}), "commit-msg") {
		t.Error("a workspace with undercover = true must get the commit-message rule")
	}
}

// ratchet: test_removed TestClaudeMDBlock_NamesTheInstalledSkillPath: the block no longer names a per-box path; TestClaudeMDBlock_IsTheSameTextOnEveryBox pins the box-independent skill path instead
// ratchet: test_removed TestClaudeMDBlock_OmitsSkillPathWhenMissing: whether this box has the skill written no longer changes the block; TestClaudeMDBlock_IsTheSameTextOnEveryBox renders both cases and demands one text
// The block is committed into a repo's CLAUDE.md, and every box that runs
// install renders it again (issue #874). Text that varies with the box — the
// queue-shim dir, the resolved skill path, whether the skill is written yet —
// re-dirties that tracked file on the next install anywhere else, so the tree
// never settles. Rendered under two config dirs, one holding the skill and one
// empty, the block must be byte-identical and name neither dir; a subagent,
// which reads project instructions but no session-start context, still learns
// where the skill lives from the box-independent path.
func TestClaudeMDBlock_IsTheSameTextOnEveryBox(t *testing.T) {
	withSkill := t.TempDir()
	if _, err := WriteTDDSkill(withSkill); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", withSkill)
	here := ClaudeMDBlock(BlockFlags{})
	empty := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", empty)
	there := ClaudeMDBlock(BlockFlags{})

	if here != there {
		t.Errorf("the block depends on the box it was rendered on:\n--- with the skill\n%s\n--- without\n%s", here, there)
	}
	for _, dir := range []string{withSkill, shellPath(withSkill), empty, shellPath(empty)} {
		if strings.Contains(here, dir) {
			t.Errorf("the block names the box path %q", dir)
		}
	}
	if !strings.Contains(here, "`~/.claude/skills/tdd/SKILL.md`") {
		t.Errorf("the block must name the tdd skill by its box-independent path:\n%s", here)
	}
}

// A repo with no Cargo.toml declares its keys in aphrollo.toml, and the
// commit-msg gate reads undercover from there. The block read only the Cargo
// metadata, so such a repo was refused an attribution trailer by a rule its
// own CLAUDE.md never stated — this repo's committed block among them.
func TestManagedBlockFor_ReadsUndercoverFromAphrolloToml(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")

	if block := managedBlockFor(repo); !strings.Contains(block, "commit-msg") {
		t.Errorf("undercover = true in aphrollo.toml must reach the block:\n%s", block)
	}
}

func TestClaudeMDBlock_NamesTheHandTypedMutationRunNotOnlyTheSpawnedOne(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{})
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
	block := ClaudeMDBlock(BlockFlags{})
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
	block := ClaudeMDBlock(BlockFlags{})
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
	block := ClaudeMDBlock(BlockFlags{})

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
	block := ClaudeMDBlock(BlockFlags{})
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
	block := ClaudeMDBlock(BlockFlags{})

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
	block := ClaudeMDBlock(BlockFlags{})
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
	changed, err := WriteClaudeMD(repo, false)
	if err != nil || changed {
		t.Fatalf("a repo with no CLAUDE.md must be left alone (changed=%v err=%v)", changed, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("no file must be invented")
	}

	changed, err = WriteClaudeMD(repo, true)
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

	if changed, err := WriteClaudeMD(repo, false); err != nil || !changed {
		t.Fatalf("first run: changed=%v err=%v", changed, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "commit-msg") {
		t.Error("the workspace's undercover flag must reach the block")
	}

	if changed, err := WriteClaudeMD(repo, false); err != nil || changed {
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

// The merge line used to state the mutation rule as a conditional — "with
// `mutants-at-merge = true` the pre-merge gate runs …" — which is a sentence
// about the tool rather than about THIS repo. A reader then has to go and
// find out which half applies to them, and the block exists so they do not
// have to. It is the third gap issue #584 names: the block takes no input
// from the repo's own config, so the merge line reads the same whether the
// repo declares the key or not.
func TestManagedBlockFor_MergeLineStatesWhatThisRepoActuallyRequires(t *testing.T) {
	measured := t.TempDir()
	mustWrite(t, filepath.Join(measured, "aphrollo.toml"), "[aphrollo]\nmutants-at-merge = true\n")
	plain := t.TempDir()
	mustWrite(t, filepath.Join(plain, "aphrollo.toml"), "[aphrollo]\n")

	withKey := managedBlockFor(measured)
	withoutKey := managedBlockFor(plain)

	if !strings.Contains(withKey, "runs this lane's mutation measurement") {
		t.Errorf("a repo declaring mutants-at-merge must be told its merge IS measured, got:\n%s", mergeLineOf(withKey))
	}
	if !strings.Contains(withoutKey, "declares no `mutants-at-merge`") {
		t.Errorf("a repo declaring nothing must be told its merge is NOT mutation-measured, got:\n%s", mergeLineOf(withoutKey))
	}
	if strings.Contains(withoutKey, "runs this lane's mutation measurement") {
		t.Errorf("the block must not claim a measurement this repo never runs, got:\n%s", mergeLineOf(withoutKey))
	}
}

// mergeLineOf is the one line under test, for a failure message that shows it
// rather than the whole block.
func mergeLineOf(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "A merge is measured") {
			return line
		}
	}
	return "(no merge line)"
}
