package rollback

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// shaOfABC is sha256("abc"), the FIPS 180 test vector, so the expected digest is
// not computed by the code under test.
const shaOfABC = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

// binDir lays out a bin dir holding the named files, each with the bytes "abc".
func binDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("abc"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFileSHA256_IsTheDigestOfTheFileBytes(t *testing.T) {
	dir := binDir(t, "aphrollo.exe")

	got, err := FileSHA256(filepath.Join(dir, "aphrollo.exe"))
	if err != nil || got != shaOfABC {
		t.Fatalf("FileSHA256 = (%q, %v), want %q", got, err, shaOfABC)
	}
	if _, err := FileSHA256(filepath.Join(dir, "missing.exe")); err == nil {
		t.Fatal("FileSHA256 of a missing file: want an error")
	}
}

func TestInstallsFile_SitsBesideTheBinaryWhateverItsExtension(t *testing.T) {
	for in, want := range map[string]string{
		"aphrollo.exe": "aphrollo.installs.json",
		"aphrollo":     "aphrollo.installs.json",
	} {
		if got := InstallsFile(in); got != want {
			t.Errorf("InstallsFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstalls_ReadBackWhatWasSaved(t *testing.T) {
	dir := binDir(t, "aphrollo.exe", "aphrollo.stale-1700000001.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	active := Binary{File: "aphrollo.exe", Commit: commitA, BuiltAt: "2026-10-02T09:00:00Z", Ref: "origin/main", SHA256: shaOfABC, InstalledAt: "2026-10-02T09:01:00Z"}
	kept := Binary{File: "aphrollo.stale-1700000001.exe", Commit: commitB, BuiltAt: "2026-10-01T09:00:00Z", Ref: "v1.3.0", SHA256: shaOfABC}
	in.Put(active)
	in.Put(kept)
	if err := in.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := OpenInstalls(dir, "aphrollo.exe")

	if want := []Binary{active, kept}; !reflect.DeepEqual(got.Binaries, want) {
		t.Fatalf("Binaries = %+v, want %+v", got.Binaries, want)
	}
	if rec, ok := got.Get("aphrollo.exe"); !ok || rec != active {
		t.Fatalf("Get(active) = (%+v, %v), want %+v", rec, ok, active)
	}
}

// The record is read beside a directory other things rename and delete files
// in, so an entry whose file is gone describes nothing and is dropped.
func TestInstalls_DropEntriesWhoseFileIsGone(t *testing.T) {
	dir := binDir(t, "aphrollo.exe", "aphrollo.stale-1700000001.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.exe", Commit: commitA, SHA256: shaOfABC})
	in.Put(Binary{File: "aphrollo.stale-1700000001.exe", Commit: commitB, SHA256: shaOfABC})
	if err := in.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "aphrollo.stale-1700000001.exe")); err != nil {
		t.Fatal(err)
	}

	got := OpenInstalls(dir, "aphrollo.exe")

	if len(got.Binaries) != 1 || got.Binaries[0].File != "aphrollo.exe" {
		t.Fatalf("Binaries = %+v, want only the file still on disk", got.Binaries)
	}
}

func TestInstalls_PutReplacesTheEntryOfTheSameFile(t *testing.T) {
	in := OpenInstalls(binDir(t), "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.exe", Commit: commitA})

	in.Put(Binary{File: "aphrollo.exe", Commit: commitB})

	if len(in.Binaries) != 1 || in.Binaries[0].Commit != commitB {
		t.Fatalf("Binaries = %+v, want the one entry holding the later commit", in.Binaries)
	}
}

// A swap reclaims copies after the record was read, so before saving it the
// record forgets whatever is no longer on disk.
func TestInstalls_DropMissingForgetsFilesRemovedSinceItWasRead(t *testing.T) {
	dir := binDir(t, "aphrollo.exe", "aphrollo.stale-1.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.exe", Commit: commitA})
	in.Put(Binary{File: "aphrollo.stale-1.exe", Commit: commitB})
	if err := os.Remove(filepath.Join(dir, "aphrollo.stale-1.exe")); err != nil {
		t.Fatal(err)
	}

	in.DropMissing()

	if len(in.Binaries) != 1 || in.Binaries[0].File != "aphrollo.exe" {
		t.Fatalf("Binaries = %+v, want only the file still on disk", in.Binaries)
	}
}

func TestInstalls_NewerFormatIsNeitherReadNorOverwritten(t *testing.T) {
	dir := binDir(t, "aphrollo.exe")
	path := filepath.Join(dir, InstallsFile("aphrollo.exe"))
	newer := `{"schema":2,"binaries":[{"file":"aphrollo.exe","commit":"` + commitA + `"}]}`
	if err := os.WriteFile(path, []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}

	in := OpenInstalls(dir, "aphrollo.exe")

	if len(in.Binaries) != 0 {
		t.Fatalf("read a newer-format record: %+v", in.Binaries)
	}
	in.Put(Binary{File: "aphrollo.exe", Commit: commitB})
	if err := in.Save(); err == nil {
		t.Fatal("Save over a newer-format record: want an error")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != newer {
		t.Fatalf("the newer record changed: %q (%v)", got, err)
	}
}

// ByCommit is what lets a rollback skip the build, so it vouches for the copy it
// returns: the commit matches, the file is there and its bytes are the ones
// recorded.
func TestInstalls_ByCommitReturnsOnlyAVerifiedKeptCopy(t *testing.T) {
	dir := binDir(t, "aphrollo.exe", "aphrollo.stale-1700000001.exe", "aphrollo.stale-1700000002.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.exe", Commit: commitA, SHA256: shaOfABC})
	in.Put(Binary{File: "aphrollo.stale-1700000001.exe", Commit: commitB, SHA256: "0000"}) // bytes differ from the record
	in.Put(Binary{File: "aphrollo.stale-1700000002.exe", Commit: commitB, SHA256: shaOfABC})

	if got, ok := in.ByCommit(commitB, "aphrollo.exe"); !ok || got.File != "aphrollo.stale-1700000002.exe" {
		t.Fatalf("ByCommit(B) = (%+v, %v), want the copy whose bytes match its record", got, ok)
	}
	if got, ok := in.ByCommit(commitA, "aphrollo.exe"); ok {
		t.Fatalf("ByCommit(A) = %+v, want nothing: the only copy is the active binary", got)
	}
	if got, ok := in.ByCommit("3333333333333333333333333333333333333333", "aphrollo.exe"); ok {
		t.Fatalf("ByCommit(unknown) = %+v, want nothing", got)
	}
}

func TestInstalls_ByCommitPrefersTheLaterOfTwoCopiesOfOneCommit(t *testing.T) {
	dir := binDir(t, "aphrollo.stale-1700000001.exe", "aphrollo.stale-1700000002.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.stale-1700000001.exe", Commit: commitB, SHA256: shaOfABC})
	in.Put(Binary{File: "aphrollo.stale-1700000002.exe", Commit: commitB, SHA256: shaOfABC})

	if got, ok := in.ByCommit(commitB, "aphrollo.exe"); !ok || got.File != "aphrollo.stale-1700000002.exe" {
		t.Fatalf("ByCommit = (%+v, %v), want the later copy", got, ok)
	}
}

func TestInstalls_OthersListsTheKeptCopiesWithoutTheActiveOne(t *testing.T) {
	dir := binDir(t, "aphrollo.exe", "aphrollo.stale-1700000001.exe")
	in := OpenInstalls(dir, "aphrollo.exe")
	in.Put(Binary{File: "aphrollo.stale-1700000001.exe", Commit: commitB})
	in.Put(Binary{File: "aphrollo.exe", Commit: commitA})

	got := in.Others("aphrollo.exe")

	if len(got) != 1 || got[0].Commit != commitB {
		t.Fatalf("Others = %+v, want only the kept copy", got)
	}
}
