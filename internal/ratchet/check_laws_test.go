package ratchet

import (
	"path/filepath"
	"testing"
)

// Laws runs a named SET in one pass: the post-edit judge's per-file laws
// share one scan instead of one Check each.
func TestCheck_LawsRunsExactlyTheNamedSet(t *testing.T) {
	root := repoWithNanGuard(t)
	writeLaw(t, root, "big-file", `
name = "big-file"
description = "modules stay small"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**"]

[matcher]
kind = "line-count"
max = 1
`)
	writeLaw(t, root, "no-todo", `
name = "no-todo"
description = "no TODO"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	write(t, filepath.Join(root, "crates", "a", "src", "big.rs"), "let a = 1;\nlet b = 2; // TODO\n")

	res, err := Check(Options{Root: root, Laws: []string{"big-file", "nan-guard"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Laws != 2 {
		t.Fatalf("laws = %d, want the two named", res.Laws)
	}
	for _, f := range res.Findings {
		if f.Law == "no-todo" {
			t.Fatalf("no-todo was not named but judged: %+v", res.Findings)
		}
	}
}
