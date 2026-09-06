package ratchet

import (
	"fmt"
	"strings"
	"testing"
)

// Validation for #492: file-set-containment's split subset_capture/
// superset_capture fields.

const containmentLawBody = `
name = "c"
description = "d"
severity = "deny"

[scope]
include = ["**/*"]

[matcher]
kind = "file-set-containment"
superset_file = "REGISTRY.md"
subset_file = "Cargo.toml"
%s
`

// TestLoadLaws_ContainmentCaptureAndSplitCapturesAreExclusive proves a law
// naming both the shared `capture` shorthand and one of the split fields is
// rejected rather than silently preferring one — preferring `capture` would
// leave superset_capture's stated notation uncompared against anything.
func TestLoadLaws_ContainmentCaptureAndSplitCapturesAreExclusive(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", fmt.Sprintf(containmentLawBody,
		"capture = \"(\\\\w+)\"\nsuperset_capture = \"`(\\\\w+)`\""))
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("err = nil, want a rejection: capture and superset_capture are exclusive")
	}
	if !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("err = %q, want it to name the two fields as exclusive", err.Error())
	}
}

// TestLoadLaws_ContainmentSplitCaptureRequiresBothSides proves one split
// field alone is rejected: subset_capture with no superset_capture cannot
// extract a comparable set from the other side.
func TestLoadLaws_ContainmentSplitCaptureRequiresBothSides(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", fmt.Sprintf(containmentLawBody, "subset_capture = \"(\\\\w+)\""))
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("err = nil, want a rejection: subset_capture with no superset_capture")
	}
	if !strings.Contains(err.Error(), "must both be given") {
		t.Fatalf("err = %q, want it to say both fields are required together", err.Error())
	}
}

// TestLoadLaws_ContainmentRequiresACaptureField proves a law naming neither
// `capture` nor the split pair is rejected at load, not left to fail later
// with a nil-pointer panic the first time a fixture runs it.
func TestLoadLaws_ContainmentRequiresACaptureField(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", fmt.Sprintf(containmentLawBody, ""))
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("err = nil, want a rejection: no capture field at all")
	}
	if !strings.Contains(err.Error(), "is required") {
		t.Fatalf("err = %q, want it to say a capture field is required", err.Error())
	}
}
