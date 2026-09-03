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
