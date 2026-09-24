package ratchet

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

// These tests cover issue #164: a file or directory the engine could not
// READ (locked, permission-denied, any I/O error) must not be treated the
// same as one that legitimately vanished mid-walk (fs.ErrNotExist). The
// first case must refuse the verdict; the second stays silent, unchanged.
//
// Making a real file or directory unreadable on disk is OS-specific (a
// Windows handle lock and a POSIX permission bit are different mechanisms,
// and neither is reliable to construct from a portable test — see the box
// probe in the issue writeup). Instead these tests inject the failure
// through the readFile/readDir seam in check.go, which every affected call
// site now goes through, so the behavior is proven identically on every
// platform the suite runs on.

// withFakeReadFile temporarily replaces the readFile seam and restores it
// unconditionally, so a failing assertion never leaves other tests reading
// through a stub.
func withFakeReadFile(t *testing.T, fn func(string) ([]byte, error)) {
	t.Helper()
	original := readFile
	readFile = fn
	t.Cleanup(func() { readFile = original })
}

// withFakeReadDir does the same for the readDir seam.
func withFakeReadDir(t *testing.T, fn func(string) ([]fs.DirEntry, error)) {
	t.Helper()
	original := readDir
	readDir = fn
	t.Cleanup(func() { readDir = original })
}

var errSimulatedLock = errors.New("simulated: locked by another process")

func TestScanTree_RefusesAVerdictWhenAScopedFileCannotBeRead(t *testing.T) {
	withFakeReadFile(t, func(string) ([]byte, error) { return nil, errSimulatedLock })

	root := t.TempDir()
	opts := Options{Root: root, Files: []string{"a.go"}}
	laws := []Law{{Name: "x", Scope: Scope{Include: []string{"*.go"}}}}

	scan, err := scanTree(opts, laws)
	if err == nil {
		t.Fatal("scanTree reported clean over a file it could not read — the violation it might carry is now invisible")
	}
	if scan != nil {
		t.Errorf("scan = %+v, want nil on refusal", scan)
	}
	if !errors.Is(err, errSimulatedLock) {
		t.Errorf("err = %v, want it to wrap the underlying read error", err)
	}
}

func TestScanTree_StaysSilentWhenAScopedFileVanishedMidWalk(t *testing.T) {
	withFakeReadFile(t, func(string) ([]byte, error) {
		return nil, fmt.Errorf("open a.go: %w", fs.ErrNotExist)
	})

	root := t.TempDir()
	opts := Options{Root: root, Files: []string{"a.go"}}
	laws := []Law{{Name: "x", Scope: Scope{Include: []string{"*.go"}}}}

	scan, err := scanTree(opts, laws)
	if err != nil {
		t.Fatalf("scanTree: %v — a genuinely vanished file must not fail the run", err)
	}
	if len(scan.files) != 0 {
		t.Errorf("files = %v, want the vanished file dropped, not reported present", scan.files)
	}
	if scan.read != 0 {
		t.Errorf("read = %d, want 0 — nothing was actually read", scan.read)
	}
}

func TestCollectFiles_RefusesAVerdictWhenADirectoryCannotBeRead(t *testing.T) {
	withFakeReadDir(t, func(string) ([]fs.DirEntry, error) { return nil, errSimulatedLock })

	laws := []Law{{Name: "x", Scope: Scope{Include: []string{"**/*.go"}}}}
	_, _, err := collectFiles(Options{Root: t.TempDir()}, laws)
	if err == nil {
		t.Fatal("collectFiles pruned an unreadable subtree with no finding — any violation under it is now invisible")
	}
	if !errors.Is(err, errSimulatedLock) {
		t.Errorf("err = %v, want it to wrap the underlying read error", err)
	}
}

func TestCollectFiles_StaysSilentWhenADirectoryVanishedMidWalk(t *testing.T) {
	withFakeReadDir(t, func(string) ([]fs.DirEntry, error) {
		return nil, fmt.Errorf("open dir: %w", fs.ErrNotExist)
	})

	laws := []Law{{Name: "x", Scope: Scope{Include: []string{"**/*.go"}}}}
	files, _, err := collectFiles(Options{Root: t.TempDir()}, laws)
	if err != nil {
		t.Fatalf("collectFiles: %v — a genuinely vanished directory must not fail the run", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %v, want none — the whole tree vanished before the walk started", files)
	}
}

func TestJSONCeilingHits_RefusesAVerdictWhenAScopedFileCannotBeRead(t *testing.T) {
	root := criterionTree(t, "46.3")
	law := jsonCeilingLaw(t, root)

	withFakeReadFile(t, func(string) ([]byte, error) { return nil, errSimulatedLock })

	hits, err := jsonCeilingHits(diskView(root), law, true, "")
	if err == nil {
		t.Fatalf("jsonCeilingHits reported %d hit(s) over a file it could not read — a real ceiling breach is now invisible", len(hits))
	}
	if !errors.Is(err, errSimulatedLock) {
		t.Errorf("err = %v, want it to wrap the underlying read error", err)
	}
}

func TestGlobFiles_RefusesAVerdictWhenADirectoryCannotBeRead(t *testing.T) {
	withFakeReadDir(t, func(string) ([]fs.DirEntry, error) { return nil, errSimulatedLock })

	files, err := globFiles(t.TempDir(), "**/*.json")
	if err == nil {
		t.Fatalf("globFiles = %v over an unreadable tree, want a refusal — any matching file under it is now invisible", files)
	}
	if !errors.Is(err, errSimulatedLock) {
		t.Errorf("err = %v, want it to wrap the underlying read error", err)
	}
}
