package ratchet

import (
	"path/filepath"
	"testing"
)

// #492's first half: file-set-containment's split subset_capture/
// superset_capture fields, proved end to end through the fixture harness
// (RunFixtures) with clean and hit cases, the same shape a real law's
// `.ratchet/fixtures/<name>` would use. No real `.ratchet/laws/*.toml` lands
// for this here: this repo's own commit gate judges its laws through the
// globally installed aphrollo binary, which predates subset_capture/
// superset_capture, and declaring one would reject every commit until that
// binary is rebuilt on merge — the same reason #315/#318's laws stayed
// fixture-only (see wholetree_hunkregex_318_test.go).
const splitCaptureFixtureLaw = `
name = "crate_registered_split_capture"
description = "every crates/<name> workspace member is documented in CRATES.md"
severity = "deny"

[scope]
include = ["**/*"]

[matcher]
kind = "file-set-containment"
superset_file = "CRATES.md"
subset_file = "Cargo.toml"
subset_capture = "\"crates/([a-z_]+)\""
superset_capture = "` + "`([a-z_]+)`" + `"
`

func TestRunFixtures_ProvesTheSplitCaptureContainmentLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "crate_registered_split_capture", splitCaptureFixtureLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "crate_registered_split_capture")

	write(t, filepath.Join(fx, "hit", "Cargo.toml"),
		"[workspace]\nmembers = [\"crates/zone\", \"crates/item\"]\n")
	write(t, filepath.Join(fx, "hit", "CRATES.md"), "| `zone` | allegiance |\n")
	write(t, filepath.Join(fx, "expected.txt"), "CRATES.md | item\n")

	write(t, filepath.Join(fx, "clean", "Cargo.toml"),
		"[workspace]\nmembers = [\"crates/zone\"]\n")
	write(t, filepath.Join(fx, "clean", "CRATES.md"), "| `zone` | allegiance |\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}
