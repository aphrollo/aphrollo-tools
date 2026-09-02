package tdd

import (
	"path/filepath"
	"testing"
)

// A law only half understood must not read as a law obeyed. The gate says so
// on stderr and leaves `ratchet-law-newer:<law>` in gate.log, so `gate stats`
// can count a rule this binary is too old to enforce fully.
func TestRatchetStageLogsALawFromANewerSchema(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawTree(t, "deny")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "future-law.toml"), `
schema = 99
name = "future-law"
description = "a law from a later schema"
severity = "deny"
future_root_key = "whatever this means later"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.unwrap\\("
`)
	gitAddAll(t, root)
	commitAll(t, root)

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("a newer law is never a hard error: %s", res.Message)
	}
	requireLoggedVerdict(t, cfg, "ratchet-law-newer:future-law")
}
