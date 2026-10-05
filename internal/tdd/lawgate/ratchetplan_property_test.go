package lawgate

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"pgregory.net/rapid"
)

// The planner's promise, held against the real judges rather than the plan
// alone (issue #968): for the files an edit wrote, the deny findings the
// post-edit judge names are exactly the ones the commit stage reaches, for any
// content those files can hold. The laws cover each shape the judge handles by
// a different route: a per-file pattern, a size ceiling, a marker window and a
// diff-scoped removal.

const planpropLaws = `
name = "planprop_todo"
description = "no TODO"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`

const planpropSize = `
name = "planprop_size"
description = "files stay short"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "line-count"
max = 5
`

const planpropMarker = `
name = "planprop_marker"
description = "unsafe needs a marker"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "marker-within-lines"
trigger = "unsafe"
marker = "// safe:"
lines = 1
`

const planpropRemoved = `
name = "planprop_removed"
description = "a removed test needs a tombstone"
severity = "deny"

[scope]
include = ["**/*_test.go"]

[matcher]
kind = "symbol-removed"
pattern = "^func (Test[A-Za-z0-9_]*)\\("
`

var planpropLines = []string{
	"package a",
	"func TestA_one(t *testing.T) {}",
	"func TestA_two(t *testing.T) {}",
	"// TODO fix #",
	"x# := unsafe",
	"// safe: reviewed",
	"var y# = 1",
	"",
}

var planpropLawNames = []string{"planprop_todo", "planprop_size", "planprop_marker", "planprop_removed"}

var planpropFiles = []string{"a/a_test.go", "c/c_test.go", "b/b.go"}

func planpropRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	for name, body := range map[string]string{
		"planprop_todo": planpropLaws, "planprop_size": planpropSize,
		"planprop_marker": planpropMarker, "planprop_removed": planpropRemoved,
	} {
		mustWrite(t, filepath.Join(root, ".ratchet", "laws", name+".toml"), body)
	}
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n\nfunc TestA_one(t *testing.T) {}\n\nfunc TestA_two(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "c", "c_test.go"), "package c\n\nfunc TestC_one(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "b", "b.go"), "package b\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

// planpropPairs is the (law, file) pairs of deny findings in the edited files.
func planpropPairs(findings []ratchet.Finding, edited map[string]bool) []string {
	seen := map[string]bool{}
	// A finding is reported at the first file holding its key and lists the others
	// in Files: it stands for every one of them.
	for _, f := range findings {
		if f.Severity != ratchet.Deny.String() {
			continue
		}
		if edited[f.File] {
			seen[f.Law+" "+f.File] = true
		}
		for _, c := range f.Files {
			if edited[c.File] {
				seen[f.Law+" "+c.File] = true
			}
		}
	}
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func TestEditLawRefusals_AreTheCommitStagesDenyFindingsAtTheEditedFiles(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := planpropRepo(t)
	rapid.Check(t, func(rt *rapid.T) {
		var rels []string
		edited := map[string]bool{}
		for _, rel := range planpropFiles {
			if !rapid.Bool().Draw(rt, "edit "+rel) {
				continue
			}
			lines := rapid.SliceOfN(rapid.SampledFrom(planpropLines), 0, 9).Draw(rt, "content "+rel)
			// Each line is spelled for its file, so no two files share a baseline key: a
			// finding stands for every file holding its key and a line names the first.
			for i := range lines {
				lines[i] = strings.ReplaceAll(lines[i], "#", string(rune('a'+len(rels))))
			}
			mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), strings.Join(lines, "\n")+"\n")
			rels = append(rels, rel)
			edited[rel] = true
		}
		if len(rels) == 0 {
			return
		}

		var editPairs []string
		for _, line := range editLawRefusals(root, rels) {
			for _, law := range planpropLawNames {
				for _, rel := range rels {
					if strings.HasPrefix(line, law+": "+rel) {
						editPairs = append(editPairs, law+" "+rel)
					}
				}
			}
		}
		sort.Strings(editPairs)
		editPairs = slices.Compact(editPairs)

		res, err := ratchet.Check(ratchet.Plan{Root: root, Stage: ratchet.StageCommit, Base: "HEAD", Files: rels}.Options())
		if err != nil {
			rt.Fatal(err)
		}
		commitPairs := planpropPairs(res.Findings, edited)

		if strings.Join(editPairs, "|") != strings.Join(commitPairs, "|") {
			rt.Fatalf("edit refusals %v differ from the commit stage's %v", editPairs, commitPairs)
		}
	})
}
