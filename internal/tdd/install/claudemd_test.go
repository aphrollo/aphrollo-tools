package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestClaudeMDBlockCarriesTheOperatingInstructions(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{Cargo: true})
	for _, want := range []string{
		claudeMDBegin, claudeMDEnd,
		"gate:", "QUEUED-SKIPPED", "not tested", "cargo check -p",
		"aphrollo <verb> --help", "aphrollo gate output", "aphrollo install",
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
	if n := strings.Count(block, "\n"); n < 8 || n > 25 {
		t.Errorf("block is %d lines — it has to be readable in one glance", n)
	}
	if strings.Contains(block, "commit-msg") {
		t.Error("a workspace that did not ask for the undercover rule must not be told it")
	}
	if !strings.Contains(ClaudeMDBlock(BlockFlags{Undercover: true}), "commit-msg") {
		t.Error("a workspace with undercover = true must get the commit-message rule")
	}
}

// How much machinery a request gets is set by the request: an answer or a
// small change is the session's own work, and only a plan written up with
// /sdd earns a builder and a cold reviewer. A block that told every session to
// orchestrate turned "check the database" into lanes, builders and reviews.
func TestClaudeMDBlock_ScalesOrchestrationToTheRequest(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{})
	for _, want := range []string{
		"Two modes", "Ad hoc", "no subagents, reviewer or plan",
		"An answer needs no lane", "aphrollo workspace create . lane/<name>", "aphrollo workspace merge --wait",
		"Planned", "`/sdd`", "a cold reviewer", "go from ad hoc to planned only if the user agrees",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not say %q:\n%s", want, block)
		}
	}
	for _, gone := range []string{"coordinator", "never edits", "delegate", "resume its builder"} {
		if strings.Contains(block, gone) {
			t.Errorf("the block still implies the main session must delegate: %q", gone)
		}
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
		"work in a lane",
		"aphrollo gate allow primary",
		"aphrollo install --managed-block-only --repo <lane>",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not state %q", want)
		}
	}
}

// ratchet: test_removed TestClaudeMDBlock_NamesTheHandTypedMutationRunNotOnlyTheSpawnedOne: the block is held to its token cap and no longer carries a merge line for a repo that declares no mutation measurement
// ratchet: test_removed TestClaudeMDBlock_CIAndCommitBothMeasureAndTheRulesSayWhich: the mutation rules no longer say who measures; the merge and commit lines each say their own gate

// A repo that measures mutants keeps its builders' mutation rules only in the
// block, so install writes the block there even with no CLAUDE.md to put it
// in; a repo that measures nothing still gets no file invented.
func TestWriteClaudeMD_CreatesTheFileInARepoThatMeasuresMutants(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"mutants-at-merge", "mutants-before-pr"} {
		repo := t.TempDir()
		mustWrite(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\n"+key+" = true\n")

		changed, err := WriteClaudeMD(repo, false)

		if err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v, want the block written", key, changed, err)
		}
		data, err := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
		if err != nil || !strings.HasPrefix(string(data), claudeMDBegin) {
			t.Errorf("%s: CLAUDE.md = %q (%v), want the managed block", key, data, err)
		}
	}
}

// The merge line used to state the mutation rule as a conditional, which is a
// sentence about the tool rather than about THIS repo. A reader then has to go
// and find out which half applies to them, and the block exists so they do not
// have to. It is the third gap issue #584 names: the block takes no input from
// the repo's own config, so the merge line read the same whether the repo
// declared the key or not. A repo that declares nothing is told nothing.
func TestManagedBlockFor_MergeLineStatesWhatThisRepoActuallyRequires(t *testing.T) {
	measured := t.TempDir()
	mustWrite(t, filepath.Join(measured, "aphrollo.toml"), "[aphrollo]\nmutants-at-merge = true\n")
	plain := t.TempDir()
	mustWrite(t, filepath.Join(plain, "aphrollo.toml"), "[aphrollo]\n")

	if got := mergeLineOf(managedBlockFor(measured)); !strings.Contains(got, "refuses an unaccepted mutant survivor") {
		t.Errorf("a repo declaring mutants-at-merge must be told its merge measures, got %q", got)
	}
	if got := mergeLineOf(managedBlockFor(plain)); got != "" {
		t.Errorf("a repo declaring nothing must get no merge line, got %q", got)
	}
}

// The builder agent is one file per user, so the rules that exist only
// because a merge or a PR measures mutants cannot live there without reaching
// every repo (issue #875). The repo's own block carries them, and only when
// the repo declares either switch and pins a level that refuses.
func TestClaudeMDBlock_StatesTheMutationRulesOnlyWhereTheRepoMeasures(t *testing.T) {
	t.Parallel()
	rules := []string{"aphrollo gate mutants prove", "--want-fail", "KILLED", "scan index"}
	for _, rule := range rules {
		if block := ClaudeMDBlock(BlockFlags{}); strings.Contains(block, rule) {
			t.Errorf("a repo that measures no mutants is told %q", rule)
		}
	}
	for name, f := range map[string]BlockFlags{
		"mutants-at-merge":  {MutantsAtMerge: true, MutantsAtMergeBlock: true},
		"mutants-before-pr": {MutantsBeforePR: true, MutantsAtMergeBlock: true},
	} {
		block := ClaudeMDBlock(f)
		for _, rule := range rules {
			if !strings.Contains(block, rule) {
				t.Errorf("a repo declaring %s is not told %q", name, rule)
			}
		}
	}
}

// A repo that pins the commit-time measurement is told a survivor refuses the
// commit, keeps the mutation rules, and a repo that does not is told nothing
// about it.
func TestClaudeMDBlock_ARepoThatMeasuresAtCommitSaysSo(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{MutantsAtCommit: true, MutantsAtCommitBlock: true})
	for _, want := range []string{"**Commit:**", "refuses it", "aphrollo gate mutants prove"} {
		if !strings.Contains(block, want) {
			t.Errorf("a repo measuring at commit is not told %q:\n%s", want, block)
		}
	}
	if strings.Contains(ClaudeMDBlock(BlockFlags{MutantsAtMerge: true}), "**Commit:**") {
		t.Error("a repo that declares no mutants-at-commit is told its commits are measured")
	}
	if strings.Contains(ClaudeMDBlock(BlockFlags{}), "mutants") {
		t.Error("a repo that measures nothing is told about mutants")
	}
}

func TestManagedBlockFor_ReadsMutantsAtCommit(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nmutants-at-commit = \"block\"\n")
	if block := managedBlockFor(repo); !strings.Contains(block, "**Commit:**") {
		t.Errorf("mutants-at-commit = \"block\" must render the commit line:\n%s", block)
	}
}

func TestManagedBlockFor_ReadsMutantsBeforePR(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nmutants-before-pr = true\nmutants-at-merge-level = \"block\"\n")

	if block := managedBlockFor(repo); !strings.Contains(block, "aphrollo gate mutants prove") {
		t.Errorf("mutants-before-pr with a pinned level must bring the mutation rules into the block:\n%s", block)
	}
}

// A repo that measures in CI keeps the mutation rules, because CI still
// refuses what they prevent, and says that a merge needs CI's check.
func TestClaudeMDBlock_ARepoThatMeasuresInCISaysSoAndKeepsTheRules(t *testing.T) {
	t.Parallel()
	rules := []string{"aphrollo gate mutants prove", "--want-fail", "KILLED", "scan index"}
	for name, f := range map[string]BlockFlags{
		"at merge":      {MutantsAtMerge: true, MutantsAtMergeCI: true, MutantsAtMergeBlock: true},
		"before the PR": {MutantsBeforePR: true, MutantsBeforePRCI: true, MutantsAtMergeBlock: true},
	} {
		block := ClaudeMDBlock(f)
		for _, rule := range rules {
			if !strings.Contains(block, rule) {
				t.Errorf("%s: a repo measuring in CI is not told %q", name, rule)
			}
		}
	}
	block := ClaudeMDBlock(BlockFlags{MutantsAtMerge: true, MutantsAtMergeCI: true, MutantsAtMergeBlock: true})
	if got := mergeLineOf(block); !strings.Contains(got, "CI's `mutants-verdict` must pass") {
		t.Errorf("the merge line does not say the merge needs CI's check: %q", got)
	}
	if strings.Contains(block, "refuses an unaccepted mutant survivor") {
		t.Errorf("the merge line claims a local answer for a repo that measures in CI:\n%s", block)
	}
}

// Only the "ci" spelling of mutants-before-pr leaves the merge line alone: a
// repo that measures at merge locally is still told so.
func TestClaudeMDBlock_BeforePRInCIDoesNotChangeALocalMergeMeasurement(t *testing.T) {
	t.Parallel()
	block := ClaudeMDBlock(BlockFlags{MutantsAtMerge: true, MutantsBeforePR: true, MutantsBeforePRCI: true})

	if got := mergeLineOf(block); !strings.Contains(got, "refuses an unaccepted mutant survivor") || strings.Contains(got, "CI's") {
		t.Errorf("the merge line is wrong for a local merge measurement: %q", got)
	}
}

func TestManagedBlockFor_ReadsTheCIModes(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nmutants-at-merge = \"ci\"\n")

	if got := mergeLineOf(managedBlockFor(repo)); !strings.Contains(got, "CI's `mutants-verdict`") {
		t.Errorf("mutants-at-merge = \"ci\" must render the CI merge line, got %q", got)
	}
}

// mergeLineOf is the block's merge line, "" when it has none.
func mergeLineOf(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- **Merge:**") {
			return line
		}
	}
	return ""
}

// A repo that pinned no block level is told its findings are reports, and is
// owed none of the proof lines or the scan-index rule.
func TestClaudeMDBlock_WithoutAPinTheMutationRulesAreGuidance(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]BlockFlags{
		"ci merge":    {MutantsAtMerge: true, MutantsAtMergeCI: true},
		"commit only": {MutantsAtCommit: true},
		"before PR":   {MutantsBeforePR: true, MutantsBeforePRCI: true},
	} {
		block := ClaudeMDBlock(f)
		for _, gone := range []string{"aphrollo gate mutants prove", "scan index", "KILLED", "**Commit:**"} {
			if strings.Contains(block, gone) {
				t.Errorf("%s: a repo that pins no block is still told %q", name, gone)
			}
		}
	}
	merge := ClaudeMDBlock(BlockFlags{MutantsAtMerge: true, MutantsAtMergeCI: true})
	if got := mergeLineOf(merge); !strings.Contains(got, "reports survivors, refuses none") {
		t.Errorf("the merge line does not say the check reports: %q", got)
	}
}

func TestManagedBlockFor_ReadsThePinnedLevels(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "aphrollo.toml"),
		"[aphrollo]\nmutants-at-commit = \"block\"\nmutants-at-merge = \"ci\"\nmutants-at-merge-level = \"block\"\n")
	block := managedBlockFor(repo)
	for _, want := range []string{"**Commit:**", "must pass; a survivor fails it", "aphrollo gate mutants prove"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
}
