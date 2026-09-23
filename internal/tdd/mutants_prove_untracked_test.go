package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #745: a mutation in a NEW file nobody has `git add`ed yet was refused
// with "git diff --numstat reports no change … Common causes: the file is
// untracked, the edit landed in a different worktree, or the tree was already
// in the mutated state." Git has no baseline for an untracked file, so the
// refusal is right; but it knew which of its three causes held and listed all
// three, and never said what makes the proof possible. It was hit twice on
// the same shape before anyone read past the cause list.
//
// The check sits ahead of runner detection, so it is the same for every
// language; both arms are pinned because a fix reaching only one of them is
// this repo's recurring defect.
func TestRunMutantsProve_AnUntrackedFileIsNamedWithTheCommandThatMakesItProvable(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		body     string
		file     string
		old, new string
	}{
		{
			name:     "cargo",
			manifest: "Cargo.toml",
			body:     "[package]\nname = \"forge_jbeam\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			file:     "src/aero_nodes.rs",
			old:      "a + b", new: "a - b",
		},
		{
			name:     "go",
			manifest: "go.mod",
			body:     "module example.com/fx\n\ngo 1.22\n",
			file:     "aero/nodes.go",
			old:      "a + b", new: "a - b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			gitInit(t, root)
			write(t, root, c.manifest, c.body)
			write(t, root, "README.md", "fixture\n")
			gitDo(t, root, "add", ".")
			gitDo(t, root, "commit", "-qm", "base")
			// Written AFTER the commit and never added: the #745 shape.
			src := "fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n"
			write(t, root, filepath.FromSlash(c.file), src)

			ran := false
			var out, errb bytes.Buffer
			code := RunMutantsProve(MutantsProveOptions{
				File: filepath.Join(root, filepath.FromSlash(c.file)),
				Old:  c.old, New: c.new,
				WantFail: "add_sums",
			}, func(Runner, string) SuiteResult {
				ran = true
				return SuiteResult{}
			}, &out, &errb)
			report := out.String() + errb.String()

			if code != ExitMutantsProveRefused {
				t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d):\n%s", code, ExitMutantsProveRefused, report)
			}
			if ran {
				t.Errorf("a suite ran over a mutation git could not see:\n%s", report)
			}
			if !strings.Contains(report, c.file+" is untracked") {
				t.Errorf("the refusal does not say %s is untracked:\n%s", c.file, report)
			}
			// Staging, not intent-to-add: `git add -N` makes the file diff
			// as wholly new, mutated or not, so the numstat check would pass
			// over an unmutated file.
			if !strings.Contains(report, "git add "+c.file) {
				t.Errorf("the refusal does not name the command that gives git a baseline (git add %s):\n%s",
					c.file, report)
			}
			if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file))); err != nil {
				t.Fatal(err)
			} else if string(got) != src {
				t.Fatalf("the file was not restored byte-identically: %q", got)
			}
		})
	}
}

// The discrimination: a TRACKED file that genuinely did not change keeps the
// general cause list. Reading every empty diff as "untracked" would send the
// reader to `git add` a file git already has.
func TestRunMutantsProve_ATrackedFileWithNoDiffIsNotCalledUntracked(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, filepath.FromSlash("src/lib.rs"), "fn add(a: i32, b: i32) -> i32 {\n    a - b\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	// Stage a trailing-space variant, then put the committed text back in
	// the working tree: a mutation that writes the staged variant leaves the
	// file matching the index, an empty diff on a file git tracks.
	write(t, root, filepath.FromSlash("src/lib.rs"), "fn add(a: i32, b: i32) -> i32 {\n    a - b \n}\n")
	gitDo(t, root, "add", ".")
	write(t, root, filepath.FromSlash("src/lib.rs"), "fn add(a: i32, b: i32) -> i32 {\n    a - b\n}\n")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "src", "lib.rs"),
		Old:  "a - b\n", New: "a - b \n",
		WantFail: "add_sums",
	}, func(Runner, string) SuiteResult { return SuiteResult{} }, &out, &errb)
	report := out.String() + errb.String()

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d):\n%s", code, ExitMutantsProveRefused, report)
	}
	if !strings.Contains(report, "reports no change") {
		t.Fatalf("a tracked, unchanged file did not reach the no-change refusal:\n%s", report)
	}
	if strings.Contains(report, "src/lib.rs is untracked") || strings.Contains(report, "git add src/lib.rs") {
		t.Errorf("a tracked file was reported as untracked:\n%s", report)
	}
}
