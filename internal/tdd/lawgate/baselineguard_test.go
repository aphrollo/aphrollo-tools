package lawgate

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// gitAddPath stages exactly one path, unlike gitAddAll — needed to leave a
// sibling file deliberately untracked.
func gitAddPath(t *testing.T, root, rel string) {
	t.Helper()
	cmd := exec.Command(gitBinary(), "add", rel)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v\n%s", rel, err, out)
	}
}

func baselineRepo(t *testing.T, path, before, after string) string {
	t.Helper()
	return tddtest.BaselineRepo(t, path, before, after)
}

func TestBaselineGuardRejectsARaisedCeiling(t *testing.T) {
	root := baselineRepo(t, "crates/ratchet/tests/module_size_baseline.txt",
		"# header\ncrates/a.rs | 1048\n",
		"# header\ncrates/a.rs | 1049\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a hand-raised ceiling must reject the commit")
	}
	for _, want := range []string{"baseline-rejected", "crates/a.rs", "1048 -> 1049", "escape comment"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

func TestBaselineGuardRejectsANewKey(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/nan-guard.txt",
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ncrates/b.rs | let b = y.clamp(0.0, 1.0);\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "crates/b.rs") {
		t.Fatalf("adding a key by hand must reject: %+v", res)
	}
}

func TestBaselineGuardAllowsLoweringRemovingAndHeaderEdits(t *testing.T) {
	cases := map[string][2]string{
		"lowered":       {"crates/a.rs | 900\n", "crates/a.rs | 650\n"},
		"removed":       {"crates/a.rs | 900\ncrates/b.rs | 10\n", "crates/b.rs | 10\n"},
		"header only":   {"# old note\ncrates/a.rs | 900\n", "# new note\ncrates/a.rs | 900\n"},
		"multiset drop": {"crates/a.rs | x.sin()\ncrates/a.rs | x.sin()\n", "crates/a.rs | x.sin()\n"},
	}
	for name, c := range cases {
		root := baselineRepo(t, "crates/ratchet/tests/x_baseline.txt", c[0], c[1])
		if res := baselineStage("precommit", root); res.Blocked {
			t.Errorf("%s must pass: %s", name, res.Message)
		}
	}
}

func TestBaselineGuardCountsIdenticalMultisetLines(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/nan-guard.txt",
		"crates/a.rs | v.clamp(0.0, 1.0)\n",
		"crates/a.rs | v.clamp(0.0, 1.0)\ncrates/a.rs | v.clamp(0.0, 1.0)\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "1 -> 2") {
		t.Fatalf("a repeated identity line is one more occurrence: %+v", res)
	}
}

// A brand-new baseline file is a law being ADOPTED, reviewed as such — the
// rule is about a ceiling that already exists being raised.
func TestBaselineGuardAllowsABrandNewBaselineFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "a.txt"), "seed\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "new-law.txt"), "crates/a.rs | 5\n")
	gitAddAll(t, root)

	if res := baselineStage("precommit", root); res.Blocked {
		t.Fatalf("adopting a law must not be a raise: %s", res.Message)
	}
}

func TestBaselineGuardIgnoresFilesOutsideTheDeclaredGlobs(t *testing.T) {
	root := baselineRepo(t, "docs/notes.txt", "crates/a.rs | 1\n", "crates/a.rs | 99\n")
	if res := baselineStage("precommit", root); res.Blocked {
		t.Fatalf("only baseline files are guarded: %s", res.Message)
	}
}

// TestMatchesAnyGlob_StarDoesNotCrossASeparatorOnEitherForwardOrBackslashInput
// proves a single `*` segment glob never reaches into a nested directory,
// and that a path spelled with backslashes (as `filepath.ToSlash` would
// leave it before normalisation, or as a caller might pass by mistake)
// judges identically to the same path spelled with forward slashes. Before
// the fix, `filepath.Match` treated `*` as crossing `/` on Windows (where
// `filepath.Separator` is `\`) while stopping at it on Linux, so the same
// repo state was guarded differently depending on which OS ran the check.
func TestMatchesAnyGlob_StarDoesNotCrossASeparatorOnEitherForwardOrBackslashInput(t *testing.T) {
	globs := []string{".ratchet/baselines/*.txt"}
	nestedForward := ".ratchet/baselines/sub/dir/x.txt"
	nestedBackslash := `.ratchet\baselines\sub\dir\x.txt`
	if matchesAnyGlob(globs, nestedForward) {
		t.Errorf("matchesAnyGlob(%q, %q) = true, want false — a nested path must not match a flat *.txt glob", globs, nestedForward)
	}
	if matchesAnyGlob(globs, nestedBackslash) {
		t.Errorf("matchesAnyGlob(%q, %q) = true, want false — must judge identically for a backslash path", globs, nestedBackslash)
	}
	direct := ".ratchet/baselines/module_size.txt"
	if !matchesAnyGlob(globs, direct) {
		t.Errorf("matchesAnyGlob(%q, %q) = false, want true — a direct child must still match", globs, direct)
	}
}

func TestBaselineGuardHonoursTheWorkspaceGlobList(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"),
		"[workspace.metadata.aphrollo]\nbaselines = [\"guards/*.txt\"]\n")
	mustWrite(t, filepath.Join(root, "guards", "sizes.txt"), "crates/a.rs | 10\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "other.txt"), "crates/a.rs | 10\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "guards", "sizes.txt"), "crates/a.rs | 11\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "other.txt"), "crates/a.rs | 11\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "guards/sizes.txt") {
		t.Fatalf("a declared glob must be guarded: %+v", res)
	}
	if strings.Contains(res.Message, "other.txt") {
		t.Errorf("a declared list REPLACES the defaults: %s", res.Message)
	}
}

// lawAndBaselineRepo commits a law owning a baseline plus its initial
// baseline content, then stages both the given law text and baseline text on
// top — the shape an "adopt a widened law" commit takes.
func lawAndBaselineRepo(t *testing.T, seedLaw, seedBaseline, stagedLaw, stagedBaseline string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), seedLaw)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), seedBaseline)
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), stagedLaw)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), stagedBaseline)
	gitAddAll(t, root)
	return root
}

const nanGuardLawText = `name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\.clamp\("
`

// TestBaselineGuard_AllowsARaiseWhenTheLawsScopeChangedInTheSameCommit is the
// escape this guard closes: a law's scope widened in the SAME commit that
// carries the baseline row the widening now reaches — adopted, not rejected.
func TestBaselineGuard_AllowsARaiseWhenTheLawsScopeChangedInTheSameCommit(t *testing.T) {
	widenedLaw := strings.Replace(nanGuardLawText,
		`include = ["crates/**/*.rs"]`, `include = ["crates/**/*.rs", "tools/**/*.rs"]`, 1)
	root := lawAndBaselineRepo(t, nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		widenedLaw, "crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")

	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	res := baselineStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a raise alongside its OWN law's widened scope must be adopted, not rejected: %s", res.Message)
	}
	requireLoggedVerdict(t, cfg, "baseline-adopted:nan-guard:1")
}

// TestBaselineGuard_RefusesARaiseWhenTheLawIsUnchanged is the same raised row,
// with the law's scope and matcher untouched — still refused, exactly like
// an unrelated hand-edit.
func TestBaselineGuard_RefusesARaiseWhenTheLawIsUnchanged(t *testing.T) {
	root := lawAndBaselineRepo(t, nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("the law never changed — the same new row must still be rejected")
	}
	if !strings.Contains(res.Message, "tools/b.rs") {
		t.Errorf("message must name the offending row: %s", res.Message)
	}
}

// TestBaselineGuard_RefusesARaiseWhenOnlyACommentInsideMatcherChanged is the
// "1048 to 1049 hand edit" case this guard's own header describes: a comment
// line added inside [matcher], with no field that decides what the law
// catches actually different, must not read as the law "changing" — a
// raw-text comparison of the [matcher] section body sees new bytes and
// wrongly adopts the raise; comparing ratchet.RuleSemantics (matcher, scope,
// severity only, comments never parsed) sees no change and refuses it, same
// as an untouched law.
func TestBaselineGuard_RefusesARaiseWhenOnlyACommentInsideMatcherChanged(t *testing.T) {
	cosmeticLaw := strings.Replace(nanGuardLawText,
		`kind = "regex-absent"`, "# cosmetic note, no field below changed\nkind = \"regex-absent\"", 1)
	root := lawAndBaselineRepo(t, nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		cosmeticLaw, "crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a comment-only edit inside [matcher] must not launder a raised baseline")
	}
	if !strings.Contains(res.Message, "tools/b.rs") {
		t.Errorf("message must name the offending row: %s", res.Message)
	}
}

// TestBaselineGuard_RefusesAHandRaiseWithNoCatchUpMerge is the cold-review
// RED: a lane introduces a law and its baseline, then a LATER commit
// hand-raises the baseline with no `git merge` anywhere in the lane's
// history. The law being absent from trunk is not by itself evidence of a
// catch-up merge — only an actual merge event is — so this must stay refused
// for the entire pre-merge lifetime of the lane, not just its first commit.
func TestBaselineGuard_RefusesAHandRaiseWithNoCatchUpMerge(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a.rs"), "let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "main")
	gitFixture(t, root, "checkout", "-q", "-b", "lane")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("no merge happened anywhere in the lane's history — a hand-raised row must still be rejected")
	}
	if !strings.Contains(res.Message, "tools/b.rs") {
		t.Errorf("message must name the offending row: %s", res.Message)
	}
}

// TestBaselineGuard_AllowsARaiseOnTheCommitRightAfterAGenuineCatchUpMerge is
// the legitimate case the escape exists for: a lane introduces a law and its
// baseline, trunk moves on without the lane, the lane runs a REAL `git merge`
// of trunk, and the very next commit re-writes the baseline to cover what the
// merge brought in. Trunk never had the law, so the lane owns the baseline
// and re-writing it with the ratchet is adoption, not a hand raise.
func TestBaselineGuard_AllowsARaiseOnTheCommitRightAfterAGenuineCatchUpMerge(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a.rs"), "let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "main")
	gitFixture(t, root, "checkout", "-q", "-b", "lane")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// Trunk grows a file the lane's law will eventually see, without the lane.
	gitFixture(t, root, "checkout", "-q", "main")
	mustWrite(t, filepath.Join(root, "tools", "b.rs"), "let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// The lane catches up with a real merge, not a hand-edit.
	gitFixture(t, root, "checkout", "-q", "lane")
	gitFixture(t, root, "merge", "main", "--no-edit", "-m", "catch up with main")

	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	res := baselineStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a raise on the commit right after a genuine trunk merge must be adopted, not rejected: %s", res.Message)
	}
	requireLoggedVerdict(t, cfg, "baseline-adopted:nan-guard:1")
}

// TestBaselineGuard_RefusesARaiseWhenTheLawIsAlreadyOnTrunk is the same lane
// shape with the law committed on main BEFORE the lane branched: trunk owns
// the baseline, so the raised row is a hand raise and stays refused.
func TestBaselineGuard_RefusesARaiseWhenTheLawIsAlreadyOnTrunk(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "main")
	gitFixture(t, root, "checkout", "-q", "-b", "lane")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("trunk already carries the law — the raised row must still be rejected")
	}
	if !strings.Contains(res.Message, "tools/b.rs") {
		t.Errorf("message must name the offending row: %s", res.Message)
	}
}

// TestBaselineGuard_RefusesARaiseWhenTheTrunkCannotBeNamed: the adoption path
// asks whether trunk already carries the law, and every uncertainty must
// answer "it does" — otherwise a repository whose trunk is called something
// else adopts every hand raise. Here the only branch is `trunk`, which no
// candidate names, so the raise stays refused.
func TestBaselineGuard_RefusesARaiseWhenTheTrunkCannotBeNamed(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "trunk")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("with no nameable trunk the raise must be refused, not adopted")
	}
}

// TestBaselineGuard_RefusesARaiseWhenAStaleMasterSitsBesideTheRealTrunk: a
// branch merely NAMED master is not trunk. The remote's own default names it,
// and the law is on that branch, so the raise is a hand raise.
func TestBaselineGuard_RefusesARaiseWhenAStaleMasterSitsBesideTheRealTrunk(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "master")
	gitFixture(t, root, "checkout", "-q", "-b", "develop")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)
	// The remote's default branch is what trunk means here, and `develop`
	// carries the law, so a lane branched off it does not own the law.
	gitFixture(t, root, "update-ref", "refs/remotes/origin/develop", "develop")
	gitFixture(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	gitFixture(t, root, "checkout", "-q", "-b", "lane")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a stale master must not stand in for the real trunk: the raise must be refused")
	}
}

// gitFixture runs one git command inside a fixture repository, hooks off.
func gitFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitBinary(), append([]string{"-c", "core.hooksPath="}, args...)...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestBaselineGuard_RefusesARaiseWhenADifferentLawChanged proves adoption is
// scoped to the law that OWNS the baseline: touching some unrelated law's
// [matcher] must never license a raise on nan-guard's baseline.
func TestBaselineGuard_RefusesARaiseWhenADifferentLawChanged(t *testing.T) {
	root := lawAndBaselineRepo(t, nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "other.toml"), `name = "other"
description = "unrelated"
severity = "warn"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("an unrelated law's presence must never license nan-guard's raise")
	}
}

// TestBaselineGuard_RefusesARaiseEvenWhenALaterLawFalselyClaimsOwnership
// proves the out-of-band-raise path: a law file present on disk but never
// `git add`ed (no index entry at all, not merely an unchanged one) is still
// read from disk and answers "does it own this baseline" BEFORE the scan
// moves on — a later, staged, brand-new law that also happens to declare the
// same baseline path must never get to answer that question in its place. A
// scan that skipped the real, untracked owner would let the raise through on
// the imposter's say-so.
func TestBaselineGuard_RefusesARaiseEvenWhenALaterLawFalselyClaimsOwnership(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// The real owner: written to disk but never staged, so it has no git
	// index entry at all.
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ntools/b.rs | let b = y.clamp(0.0, 1.0);\n")
	// A brand-new, STAGED law that also (wrongly) declares the same
	// baseline path, sorted after nan-guard.toml so it is only ever reached
	// if the real, untracked owner gets skipped instead of consulted.
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "zzz-imposter.toml"),
		strings.Replace(nanGuardLawText, `name = "nan-guard"`, `name = "zzz-imposter"`, 1))
	gitAddPath(t, root, filepath.Join(".ratchet", "baselines", "nan-guard.txt"))
	gitAddPath(t, root, filepath.Join(".ratchet", "laws", "zzz-imposter.toml"))

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("the real, untracked owner must be consulted before any later law can claim adoption")
	}
}

func TestBaselineGuardIsSilentWhenNoBaselineIsStaged(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	gitAddAll(t, root)
	if res := baselineStage("precommit", root); res.Blocked || res.Message != "" {
		t.Fatalf("result = %+v, want silence", res)
	}
}

// TestTrunkBranch_ReturnsOriginHEADTargetAssoonAsItResolves proves the first
// candidate short-circuits: when refs/remotes/origin/HEAD names a branch,
// that name is returned immediately, without falling through to
// init.defaultBranch or the main/master fallbacks.
func TestTrunkBranch_ReturnsOriginHEADTargetAsSoonAsItResolves(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitAddAll(t, root)
	commitAll(t, root)
	// Keep the local default branch off main/master so neither fallback
	// candidate could coincidentally produce the same answer.
	gitFixture(t, root, "branch", "-M", "primary")
	gitFixture(t, root, "checkout", "-q", "-b", "develop")
	gitFixture(t, root, "update-ref", "refs/remotes/origin/develop", "develop")
	gitFixture(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")

	got := TrunkBranch(root)
	want := "origin/develop"
	if got != want {
		t.Fatalf("trunkBranch(root) = %q, want %q — the remote's own default must win immediately", got, want)
	}
}

// TestTrunkBranch_UsesConfiguredDefaultBranchWhenItResolves proves the second
// candidate: with no origin/HEAD set, a configured init.defaultBranch that
// names a real, resolvable branch is returned. This single case constrains
// all three conditions the config path chains together (config succeeded,
// the configured name is non-empty, and the name actually resolves) — break
// any one of the three and the chain falls through to the unconfigured
// main/master fallback, which finds nothing here and answers "".
func TestTrunkBranch_UsesConfiguredDefaultBranchWhenItResolves(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitAddAll(t, root)
	commitAll(t, root)
	// Keep the local default branch off main/master so neither fallback
	// candidate could coincidentally produce the same answer as "trunk".
	gitFixture(t, root, "branch", "-M", "primary")
	gitFixture(t, root, "checkout", "-q", "-b", "trunk")
	gitFixture(t, root, "config", "init.defaultBranch", "trunk")

	got := TrunkBranch(root)
	want := "trunk"
	if got != want {
		t.Fatalf("trunkBranch(root) = %q, want %q — a configured, resolvable default branch must win", got, want)
	}
}

// TestLastSHALine_RejectsANonHexByteEvenAtLength40 proves the hex-digit scan
// actually runs: a string that is exactly 40 bytes long but carries one
// non-hex byte must still be rejected. Skipping the scan would let any
// 40-byte string through length-checking alone.
func TestLastSHALine_RejectsANonHexByteEvenAtLength40(t *testing.T) {
	notHex := strings.Repeat("a", 39) + "z" // 40 bytes, 'z' is not a hex digit
	if len(notHex) != 40 {
		t.Fatalf("test fixture is %d bytes, want exactly 40", len(notHex))
	}
	if got := lastSHALine(notHex); got != "" {
		t.Fatalf("lastSHALine(%q) = %q, want \"\" — a non-hex byte must reject even at the right length", notHex, got)
	}
}
