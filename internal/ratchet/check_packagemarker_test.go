package ratchet

import (
	"path/filepath"
	"testing"
)

// packageIsolationLaw is a stand-in for test_world_leak: a trigger that
// leaks into the real environment, excused by a marker declaring isolation
// — kept minimal so the test proves the MATCHER KIND, not the real law's
// full pattern list.
const packageIsolationLaw = `
name        = "pkg-isolation"
description = "a trigger excused only when the package it lives in declares isolation"
severity    = "deny"

[scope]
include = ["**/*_test.go"]

[matcher]
kind    = "marker-in-package"
trigger = "os\\.UserHomeDir\\("
marker  = "func TestMain\\("
`

// TestMarkerInPackage_TriggerWithNoSiblingExcuseIsAHit proves the matcher
// still fires when nothing in the package — the file itself or any file
// beside it — declares the marker: a package-scoped matcher must not go
// silent just because it now looks past one file.
func TestMarkerInPackage_TriggerWithNoSiblingExcuseIsAHit(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "pkg-isolation", packageIsolationLaw)
	write(t, filepath.Join(root, "pkg", "a_test.go"), "func TestA(t *testing.T) {\n\thome, _ := os.UserHomeDir()\n\t_ = home\n}\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.File != "pkg/a_test.go" || f.Line != 2 {
		t.Errorf("finding = %+v", f)
	}
}

// TestMarkerInPackage_ExcusedBySiblingTestMainIsNotAHit proves the same
// trigger is NOT a hit once a SIBLING file in the same package declares
// TestMain — the sharpest case from #322: a package-level TestMain in a
// different file, invisible to any file-at-a-time matcher.
func TestMarkerInPackage_ExcusedBySiblingTestMainIsNotAHit(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "pkg-isolation", packageIsolationLaw)
	write(t, filepath.Join(root, "pkg", "a_test.go"), "func TestA(t *testing.T) {\n\thome, _ := os.UserHomeDir()\n\t_ = home\n}\n")
	write(t, filepath.Join(root, "pkg", "main_test.go"), "func TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a sibling TestMain must excuse the trigger: %+v", res.Findings)
	}
}

// TestMarkerInPackage_NarrowedRunReadsTheSiblingFromDisk proves the pre-edit
// shape works too: a run narrowed to the ONE file being written must still
// see an on-disk sibling's excuse, even though the walk that produced `files`
// never visited it.
func TestMarkerInPackage_NarrowedRunReadsTheSiblingFromDisk(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "pkg-isolation", packageIsolationLaw)
	write(t, filepath.Join(root, "pkg", "a_test.go"), "func TestA(t *testing.T) {}\n")
	write(t, filepath.Join(root, "pkg", "main_test.go"), "func TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n")

	proposed := "func TestA(t *testing.T) {\n\thome, _ := os.UserHomeDir()\n\t_ = home\n}\n"
	res, err := Check(Options{
		Root:     root,
		Files:    []string{"pkg/a_test.go"},
		Proposed: map[string]string{"pkg/a_test.go": proposed},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a narrowed run must still read the sibling on disk: %+v", res.Findings)
	}
}
