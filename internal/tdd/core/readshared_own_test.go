package core

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The harvest polls a result file that the wrapper replaces by rename. On
// Windows a replace over a file a reader holds open fails unless that reader
// opened it with delete sharing, and no retry window cures a reader that keeps
// the file: the reader the gate uses must let the writer through.
func TestWriteFileAtomic_ReplacesAFileASharedReaderHoldsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.result.json")
	if err := writeFileAtomic(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	held, err := openShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	start := time.Now()
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatalf("replace while a reader holds the file open: %v", err)
	}
	// A generous bound: a refused replace is retried for renameBound, so a
	// replace that waited that long was blocked by the reader, not let through.
	if took := time.Since(start); took >= renameBound {
		t.Errorf("replace took %v, want it not to wait on the reader", took)
	}

	if old, err := io.ReadAll(held); err != nil || string(old) != "old" {
		t.Errorf("the held handle reads %q, %v, want the old content", old, err)
	}
	if got, err := readFileShared(path); err != nil || string(got) != "new" {
		t.Errorf("readFileShared = %q, %v, want the new content", got, err)
	}
}

func TestReadFileShared_AMissingFileIsNotExist(t *testing.T) {
	_, err := readFileShared(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want one errors.Is reports as fs.ErrNotExist", err)
	}
}

func TestReadFileShared_ReadsTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	want := make([]byte, 3<<20+7)
	for i := range want {
		want[i] = byte(i % 251)
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readFileShared(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("read %d bytes, err %v, want all %d", len(got), err, len(want))
	}
}
