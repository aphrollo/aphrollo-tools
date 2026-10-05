package ratchet

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

var (
	planPaths   = []string{"a/x.go", "a/x_test.go", "b/y.rs", "docs/z.md", "README.md", "go.mod", "b/c/w.go"}
	planInclude = []string{"**/*.go", "**/*_test.go", "**/*.rs", "docs/**", "*.md", "go.mod", "**/*"}
	planExclude = []string{"a/**", "**/*_test.go", "b/**"}
)

// planKinds is every matcher kind the engine knows, in a fixed order.
func planKinds() []MatcherKind {
	var kinds []MatcherKind
	for k := range matcherKeys {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

func lawSetGen(t *rapid.T) []Law {
	kinds := planKinds()
	n := rapid.IntRange(0, 6).Draw(t, "laws")
	var laws []Law
	for i := range n {
		sev := Deny
		if rapid.Bool().Draw(t, "warn") {
			sev = Warn
		}
		l := Law{
			Name:     "law" + string(rune('a'+i)),
			Severity: sev,
			Scope: Scope{
				Include: rapid.SliceOfNDistinct(rapid.SampledFrom(planInclude), 1, 3, func(s string) string { return s }).Draw(t, "include"),
				Exclude: rapid.SliceOfNDistinct(rapid.SampledFrom(planExclude), 0, 2, func(s string) string { return s }).Draw(t, "exclude"),
			},
			Matcher: Matcher{Kind: rapid.SampledFrom(kinds).Draw(t, "kind")},
		}
		if rapid.Bool().Draw(t, "unknown") && rapid.Bool().Draw(t, "unknown2") {
			l.UnknownKind = "from-the-future"
		}
		laws = append(laws, l)
	}
	return laws
}

func filesGen(t *rapid.T) []string {
	return rapid.SliceOfNDistinct(rapid.SampledFrom(planPaths), 1, 5, func(s string) string { return s }).Draw(t, "files")
}

func overlayGen(t *rapid.T, files []string) map[string]string {
	if !rapid.Bool().Draw(t, "overlay") {
		return nil
	}
	return map[string]string{files[0]: "proposed"}
}

// TestPlan_EditPlanIsASubsetOfTheCommitPlan: whatever law and file the edit
// stage would judge, the commit stage judges too, for any laws and any files.
func TestPlan_EditPlanIsASubsetOfTheCommitPlan(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		laws, files := lawSetGen(t), filesGen(t)
		overlay := overlayGen(t, files)
		edit := PlanOf(laws, Plan{Stage: StageEdit, Files: files, Overlay: overlay})
		commit := PlanOf(laws, Plan{Stage: StageCommit, Files: files})
		for _, s := range edit {
			for _, f := range s.Files {
				if !commit.Covers(s.Law.Name, f) {
					t.Fatalf("edit judges %s at %s, the commit plan does not", s.Law.Name, f)
				}
			}
		}
	})
}

// TestPlan_EveryScopedLawIsInTheEditPlanUnlessCommitOnly is the #968 class:
// a law the commit gate would refuse at an edited file must have been judged
// at the edit.
func TestPlan_EveryScopedLawIsInTheEditPlanUnlessCommitOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		laws, files := lawSetGen(t), filesGen(t)
		overlay := overlayGen(t, files)
		edit := PlanOf(laws, Plan{Stage: StageEdit, Files: files, Overlay: overlay})
		for _, l := range laws {
			if l.UnknownKind != "" {
				continue
			}
			for _, f := range files {
				if !l.Scope.Matches(f) || CommitOnly(l, overlay != nil) {
					continue
				}
				if !edit.Covers(l.Name, f) {
					t.Fatalf("law %s (%s) covers %s and is not commit-only, yet the edit plan skips it", l.Name, l.Matcher.Kind, f)
				}
			}
		}
	})
}

// TestPlan_CommitOnlyKindsAreTheDependencyGraphLawsWithoutAnOverlay writes the
// commit-only laws down: the module-graph laws, which a post-edit judgement
// (no proposed content to hand the graph) leaves to the commit.
func TestPlan_CommitOnlyKindsAreTheDependencyGraphLawsWithoutAnOverlay(t *testing.T) {
	var got []string
	for _, k := range planKinds() {
		if CommitOnly(Law{Matcher: Matcher{Kind: k}}, false) {
			got = append(got, string(k))
		}
		if CommitOnly(Law{Matcher: Matcher{Kind: k}}, true) {
			t.Errorf("%s is commit-only even with proposed content", k)
		}
	}
	want := []string{"dep-graph-ceiling", "dep-graph-forbids", "go-dep-graph-forbids"}
	if len(got) != len(want) {
		t.Fatalf("commit-only kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commit-only kinds = %v, want %v", got, want)
		}
	}
}

// TestPlan_CommitPlanJudgesEveryKnownLawWhole: the commit reads the whole tree
// for every law this binary can run, a law it cannot run (UnknownKind) is not
// planned.
func TestPlan_CommitPlanJudgesEveryKnownLawWhole(t *testing.T) {
	laws := []Law{
		{Name: "known", Scope: Scope{Include: []string{"**/*.go"}}, Matcher: Matcher{Kind: KindRegexAbsent}},
		{Name: "future", UnknownKind: "x", Scope: Scope{Include: []string{"**/*.go"}}},
	}
	got := PlanOf(laws, Plan{Stage: StageCommit, Files: []string{"README.md"}})
	if len(got) != 1 || got[0].Law.Name != "known" || !got[0].Whole {
		t.Fatalf("commit plan = %+v, want the one known law judged whole", got)
	}
	if !got.Covers("known", "never/edited.go") {
		t.Fatal("a whole-judged law must cover a file the commit did not name")
	}
}

func TestPlan_EditPlanNamesOnlyTheEditedFilesInScope(t *testing.T) {
	laws := []Law{{Name: "go", Scope: Scope{Include: []string{"**/*.go"}}, Matcher: Matcher{Kind: KindRegexAbsent}}}
	got := PlanOf(laws, Plan{Stage: StageEdit, Files: []string{"a/x.go", "README.md"}})
	if len(got) != 1 || len(got[0].Files) != 1 || got[0].Files[0] != "a/x.go" || got[0].Whole {
		t.Fatalf("edit plan = %+v, want the one law over a/x.go only", got)
	}
	if got.Covers("go", "README.md") {
		t.Fatal("the edit plan covers a file outside the law's scope")
	}
}

func writePlanLaw(t *testing.T, root, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("name = \"" + name + "\"\ndescription = \"" + desc + "\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/*.go\"]\n\n[matcher]\nkind = \"regex-absent\"\npattern = \"TODO\"\n")
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadLaws_ParsesOncePerContent: a process loading the same laws again
// reads them from the first load, and a changed law is read afresh.
func TestLoadLaws_ParsesOncePerContent(t *testing.T) {
	root := t.TempDir()
	writePlanLaw(t, root, "one", "first")
	before := lawParses.Load()
	for range 3 {
		if _, err := LoadLaws(root); err != nil {
			t.Fatal(err)
		}
	}
	if n := lawParses.Load() - before; n != 1 {
		t.Fatalf("three loads of unchanged laws parsed %d times, want 1", n)
	}
	writePlanLaw(t, root, "one", "second")
	laws, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	if laws[0].Description != "second" {
		t.Fatalf("description = %q after the law changed, want \"second\"", laws[0].Description)
	}
	if n := lawParses.Load() - before; n != 2 {
		t.Fatalf("a changed law left %d parses, want 2", n)
	}
}

// TestLoadLaws_ANewScopeSetOrLanguageRowIsNotServedFromTheCache: the hash
// covers every file a law's load reads, not the law files alone.
func TestLoadLaws_ANewScopeSetOrLanguageRowIsNotServedFromTheCache(t *testing.T) {
	root := t.TempDir()
	writePlanLaw(t, root, "one", "d")
	if _, err := LoadLaws(root); err != nil {
		t.Fatal(err)
	}
	before := lawParses.Load()
	if err := os.WriteFile(filepath.Join(root, ".ratchet", "scopes.toml"), []byte("[sets]\nx = [\"**/*.go\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLaws(root); err != nil {
		t.Fatal(err)
	}
	if lawParses.Load()-before != 1 {
		t.Fatal("a new scopes.toml was served from the cache")
	}
}

// TestLoadLaws_ACachedLoadIsNotMutatedByItsCaller: Check marks the laws it is
// handed; the next caller must not see the marks.
func TestLoadLaws_ACachedLoadIsNotMutatedByItsCaller(t *testing.T) {
	root := t.TempDir()
	writePlanLaw(t, root, "one", "d")
	first, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Name = "scribbled"
	first[0].Committed = map[string]bool{"x": true}
	second, err := LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Name != "one" || second[0].Committed != nil {
		t.Fatalf("second load = %q committed=%v, want the law as it was read", second[0].Name, second[0].Committed)
	}
}

func TestPlanSteps_AMalformedLawIsAnErrorTheStageDecidesOn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.toml"), []byte("name = \"bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []Stage{StageEdit, StageCommit, StageMerge} {
		if _, err := (Plan{Root: root, Stage: stage, Files: []string{"a.go"}}).Steps(); err == nil {
			t.Errorf("stage %s: a law that does not parse planned without an error", stage)
		}
	}
}

// TestPlanOptions_TheCommitJudgesTheWholeTreeFromABase and the edit judges its
// files against themselves: the two stages read one plan into different runs.
func TestPlanOptions_TheCommitJudgesTheWholeTreeFromABase(t *testing.T) {
	overlay := map[string]string{"a.go": "staged"}
	commit := Plan{Root: "r", Stage: StageCommit, Base: "HEAD", Files: []string{"a.go"}, Overlay: overlay}.Options()
	if commit.Root != "r" || commit.Base != "HEAD" || len(commit.Files) != 0 ||
		len(commit.StagedFiles) != 1 || commit.StagedFiles[0] != "a.go" || commit.Proposed["a.go"] != "staged" {
		t.Fatalf("commit options = %+v, want the whole tree (no Files) from HEAD with a.go staged and its overlay", commit)
	}
	edit := Plan{Root: "r", Stage: StageEdit, Base: "HEAD", Files: []string{"a.go"}, Overlay: overlay}.Options()
	if edit.Base != "" || len(edit.Files) != 1 || edit.Files[0] != "a.go" || edit.Proposed["a.go"] != "staged" {
		t.Fatalf("edit options = %+v, want a.go alone against itself (no Base)", edit)
	}
}
