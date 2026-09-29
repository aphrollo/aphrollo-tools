package mutation

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeProveHolder(t *testing.T, record string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, proveSandboxHolder), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSandboxHeld_ByTheProcessThatWroteItsRecord(t *testing.T) {
	id, ok := processIdentityFn(os.Getpid())
	if !ok {
		t.Skip("this host cannot name a process's identity")
	}
	dir, err := holdSandboxDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !sandboxHeld(dir) {
		t.Errorf("a directory this process just recorded itself as holding is not held (identity %s)", id)
	}
}

func TestSandboxHeld_NotWhenTheRecordedIdentityIsAnotherProcess(t *testing.T) {
	if _, ok := processIdentityFn(os.Getpid()); !ok {
		t.Skip("this host cannot name a process's identity")
	}
	dir := writeProveHolder(t, "pid="+strconv.Itoa(os.Getpid())+"\nidentity=an-earlier-boot:1\n")
	if sandboxHeld(dir) {
		t.Error("a pid reused by another process kept a leftover held")
	}
}

func TestSandboxHeld_ARecordWithoutAnIdentityIsJudgedByItsPid(t *testing.T) {
	dir := writeProveHolder(t, "pid="+strconv.Itoa(os.Getpid())+"\n")
	if !sandboxHeld(dir) {
		t.Error("a live pid with no recorded identity is not held")
	}
}

func TestSandboxHeld_ARecordNoOneCanReadIsHeld(t *testing.T) {
	for name, record := range map[string]string{
		"no pid":       "identity=x\n",
		"a bad pid":    "pid=abc\n",
		"an empty one": "",
	} {
		if !sandboxHeld(writeProveHolder(t, record)) {
			t.Errorf("%s: a record that names no holder was treated as free", name)
		}
	}
	if !sandboxHeld(t.TempDir()) {
		t.Error("a directory with no record was treated as free")
	}
}

func TestProveAreaHeld_OnlyForAProofsOwnLiveDirectory(t *testing.T) {
	area := t.TempDir()
	if err := os.Mkdir(filepath.Join(area, "shard-0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if proveAreaHeld(area) {
		t.Error("a measurement shard directory with no record held the area")
	}
	if proveAreaHeld(filepath.Join(area, "missing")) {
		t.Error("an area that does not exist was held")
	}
	if err := os.Mkdir(filepath.Join(area, "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !proveAreaHeld(area) {
		t.Error("a run- directory with no readable record did not hold the area")
	}
}
