package tdd

import (
	"strings"
	"testing"
)

// The fail-first proof withholds the new IMPLEMENTATION and runs the new
// tests. It used to withhold the new DATA too: a staged //go:embed template,
// a .ron fixture, a golden file under tests/. Those tests then ran against
// the OLD data and went red for that reason alone, and the gate recorded a
// RED that proves nothing about the code -- the one outcome this stage exists
// to make impossible.

// TestPrecommit_FailFirst_CarriesAStagedEmbeddedTemplateIntoTheProofTree is
// the named case: the change IS the template, so the test that asserts on it
// passes the moment the template is present, and the proof must say so.
func TestPrecommit_FailFirst_CarriesAStagedEmbeddedTemplateIntoTheProofTree(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	// A git pre-commit hook runs with the repo root as its cwd, and that is
	// what ClassifyFile's //go:embed lookup resolves a staged path against.
	t.Chdir(root)
	write(t, root, "skill.go", "package m\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar skill string\n\n// Skill is the managed template.\nfunc Skill() string { return skill }\n")
	write(t, root, "skill.md", "# skill\n\nold body\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "skill")

	// The commit under judgment changes ONLY the template plus the test that
	// asserts its new content.
	write(t, root, "skill.md", "# skill\n\nnew body\n")
	write(t, root, "skill_test.go", "package m\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestSkillCarriesTheNewBody(t *testing.T) {\n\tif !strings.Contains(Skill(), \"new body\") {\n\t\tt.Fatalf(\"skill.md is stale: %q\", Skill())\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "fail-first") {
		t.Fatalf("the proof must run against the STAGED template, so the test passes and fail-first blocks; got %+v", res)
	}
}

// A staged data file no test names may BE the change under test, so it stays
// withheld: carrying it would turn a correct commit into a fail-first
// violation.
func TestPrecommit_FailFirst_WithholdsAStagedDataFileNoTestNames(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	write(t, root, "notes.md", "unrelated prose nothing reads\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidgetIsTwo(t *testing.T) {\n\tif Widget() != 2 {\n\t\tt.Fatal(Widget())\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a real RED must still pass the proof: %s", res.Message)
	}
}

func TestProofInputs_TakesTheDataATestNamesAndLeavesCodeBehind(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "skill.go", "package m\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar skill string\n")
	write(t, root, "skill.md", "body\n")
	write(t, root, "fixtures/golden.json", "{}\n")
	write(t, root, "notes.md", "prose\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\n// reads fixtures/golden.json\n")
	gitDo(t, root, "add", ".")

	got := strings.Join(proofInputs(root, []string{"widget_test.go"}), ",")
	for _, want := range []string{"skill.md", "fixtures/golden.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("proof inputs %q must carry %q", got, want)
		}
	}
	for _, unwanted := range []string{"skill.go", "widget.go", "notes.md", "widget_test.go"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("proof inputs %q must not carry %q", got, unwanted)
		}
	}
}

// A .ron table is DATA to a reader and the implementation to this gate: when
// the commit's change IS the table, applying it into the proof tree makes the
// new test pass there, and a correct commit is rejected as a fail-first
// violation. It stays withheld, exactly like the .rs beside it.
func TestProofInputs_WithholdsARonTableThisCommitChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "tables/items.ron", "(items: [])\n")
	write(t, root, "items_test.go", "package m\n\n// reads tables/items.ron\n")
	gitDo(t, root, "add", ".")

	if got := strings.Join(proofInputs(root, []string{"items_test.go"}), ","); strings.Contains(got, "items.ron") {
		t.Fatalf("proof inputs %q must withhold the table this commit writes", got)
	}
}

// A CI workflow a test pins is the implementation, never test data: the test
// that asserts the deploy job's build line reads pipeline.yml, and carrying the
// fixed workflow into the proof tree makes that test pass against HEAD, so a
// correct commit is rejected as a fail-first violation. Changed, it stays
// withheld, exactly like a .ron table.
func TestProofInputs_WithholdsAWorkflowThisCommitChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, ".github/workflows/pipeline.yml", "jobs:\n  deploy:\n    steps: []\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "pipeline")

	write(t, root, ".github/workflows/pipeline.yml", "jobs:\n  deploy:\n    steps: [build]\n")
	write(t, root, "deploy_test.go", "package m\n\n// reads .github/workflows/pipeline.yml\n")
	gitDo(t, root, "add", ".")

	if got := strings.Join(proofInputs(root, []string{"deploy_test.go"}), ","); strings.Contains(got, "pipeline.yml") {
		t.Fatalf("proof inputs %q must withhold the workflow this commit changes", got)
	}
}

// The other half: a table this commit only MOVED is unchanged data the test
// reads, and withholding it fails the new test for a stale path rather than
// for missing code.
func TestProofInputs_CarriesARonTableThisCommitOnlyMoved(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "tables/items.ron", "(items: [])\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "items table")

	gitDo(t, root, "mv", "tables/items.ron", "tables/loot.ron")
	write(t, root, "items_test.go", "package m\n\n// reads tables/loot.ron\n")
	gitDo(t, root, "add", ".")

	if got := strings.Join(proofInputs(root, []string{"items_test.go"}), ","); !strings.Contains(got, "loot.ron") {
		t.Fatalf("proof inputs %q must carry a table this commit only moved", got)
	}
}

// namesPath decides what data rides into the proof tree, so a loose match
// carries a file the tests never read -- and a file that IS the change under
// test carried in is a correct commit rejected as a fail-first violation.
func TestNamesPath_WantsTheWholeName(t *testing.T) {
	cases := []struct {
		name string
		text string
		path string
		want bool
	}{
		{"the repo-relative path", "loads tables/items.ron here", "tables/items.ron", true},
		{"the base name alone", "golden.json is the fixture", "fixtures/golden.json", true},
		{"a longer name ending in it", "reads fixtures/myitems.ron", "tables/items.ron", false},
		{"a longer name starting with it", "reads items.ron.bak", "tables/items.ron", false},
		{"a base with no extension", "the data it reads", "tables/data", false},
		{"a path inside a longer path", "vendor/tables/items.ron", "tables/items.ron", false},
	}
	for _, c := range cases {
		if got := namesPath(c.text, c.path); got != c.want {
			t.Errorf("%s: namesPath(%q, %q) = %v, want %v", c.name, c.text, c.path, got, c.want)
		}
	}
}
