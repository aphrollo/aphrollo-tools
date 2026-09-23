package mutation

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #745: a mutation in a NEW file nobody has `git add`ed yet was refused,
// because the landing check diffed the file against the index and an
// untracked file has no index entry: the diff was empty whatever the mutation
// wrote. The check now compares the content against the proof's own starting
// bytes, so an untracked file is proved like any other, with no staging step.
//
// The check sits ahead of runner detection, so it is the same for every
// language; both arms are pinned because a fix reaching only one of them is
// this repo's recurring defect.
// ratchet: test_removed TestRunMutantsProve_AnUntrackedFileIsNamedWithTheCommandThatMakesItProvable: an untracked file is no longer refused; the test below proves it instead
// ratchet: test_removed TestRunMutantsProve_ATrackedFileWithNoDiffIsNotCalledUntracked: the index diff it discriminated is gone; the landing check reads the proof's own starting bytes
func TestRunMutantsProve_AMutationInAnUntrackedFileIsProved(t *testing.T) {
	cases := []struct {
		name, manifest, body, file, failing string
	}{
		{
			name:     "cargo",
			manifest: "Cargo.toml",
			body:     "[package]\nname = \"forge_jbeam\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			file:     "src/aero_nodes.rs",
			failing:  "test aero_nodes::tests::add_sums ... FAILED\n",
		},
		{
			name:     "go",
			manifest: "go.mod",
			body:     "module example.com/fx\n\ngo 1.22\n",
			file:     "aero/nodes.go",
			failing:  "--- FAIL: add_sums (0.00s)\nFAIL\n",
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
				Old:  "a + b", New: "a - b",
				WantFail: "add_sums",
			}, func(Runner, string) SuiteResult {
				ran = true
				return SuiteResult{Output: c.failing}
			}, &out, &errb)
			report := out.String() + errb.String()

			if code != ExitMutantsProveKilled {
				t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s", code, ExitMutantsProveKilled, report)
			}
			if !ran {
				t.Errorf("no suite ran over a mutation that landed in an untracked file:\n%s", report)
			}
			if !strings.Contains(report, "add_sums") {
				t.Errorf("the verdict never names the killed test:\n%s", report)
			}
			if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file))); err != nil {
				t.Fatal(err)
			} else if string(got) != src {
				t.Fatalf("the file was not restored byte-identically: %q", got)
			}
		})
	}
}
