package gc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const vitetempName = "aB3_xYz-0123456789Qrs" // 21 characters, as a nanoid is

func vitetempDirIn(t *testing.T, root, name, sub, file string, age time.Duration) string {
	t.Helper()
	mkFile(t, filepath.Join(root, name, sub, file), "__vite_ssr_exportName__", age)
	return filepath.Join(root, name)
}

func TestViteTempName_OnlyA21CharacterIdentifierIsOne(t *testing.T) {
	for name, want := range map[string]bool{
		vitetempName:                  true,
		"aaaaaaaaaaaaaaaaaaaaa":       true,
		"aaaaaaaaaaaaaaaaaaaa":        false, // 20
		"aaaaaaaaaaaaaaaaaaaaaa":      false, // 22
		"aaaaaaaaaaaaaaaaaaa.a":       false, // a dot is no nanoid character
		"aaaaaaaaaaaaaaaaaaa a":       false,
		"node-compile-cache-12345678": false,
	} {
		if got := viteTempName(name); got != want {
			t.Errorf("viteTempName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGCViteTemp_ProposesAnIdleRunsOutputAndNothingThatLooksLikeItOnlyByName(t *testing.T) {
	root := t.TempDir()
	stale := vitetempDirIn(t, root, vitetempName, "ssr", "a1b2c3d4e5f6a7b8.js", 30*time.Hour)
	mkFile(t, filepath.Join(stale, "client", "0123456789abcdef.js"), "x", 30*time.Hour)
	fresh := vitetempDirIn(t, root, "FRESHfreshFRESHfresh1", "client", "a1b2c3d4e5f6a7b8.js", time.Hour)
	noSub := vitetempDirIn(t, root, "NOSUBnosubNOSUBnosub1", "cache", "a1b2c3d4e5f6a7b8.js", 30*time.Hour)
	extra := vitetempDirIn(t, root, "EXTRAextraEXTRAextr1", "ssr", "a1b2c3d4e5f6a7b8.js", 30*time.Hour)
	mkFile(t, filepath.Join(extra, "notes.txt"), "mine", 30*time.Hour)
	notHash := vitetempDirIn(t, root, "NOTHASHnothashNOTHAS1", "client", "x.js", 30*time.Hour)
	empty := filepath.Join(root, "EMPTYemptyEMPTYempty1")
	if err := os.MkdirAll(filepath.Join(empty, "client"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Hour)
	_ = os.Chtimes(filepath.Join(empty, "client"), old, old)
	withScratchHeld(t, func(string) (bool, bool) { return false, true })

	got := gcViteTemp(root, time.Now())

	if len(got) != 1 || got[0].Path != stale || got[0].Kind != GCKindTempLitter || got[0].Size == 0 {
		t.Fatalf("candidates = %+v, want exactly %s", got, stale)
	}
	if !strings.Contains(got[0].Reason, "vite") {
		t.Errorf("reason %q does not say what it is", got[0].Reason)
	}
	_ = []string{fresh, noSub, extra, notHash}
}

func TestGCViteTemp_NeverProposesADirectoryALiveProcessHolds(t *testing.T) {
	root := t.TempDir()
	vitetempDirIn(t, root, vitetempName, "ssr", "a1b2c3d4e5f6a7b8.js", 30*time.Hour)
	withScratchHeld(t, func(string) (bool, bool) { return true, true })

	if got := gcViteTemp(root, time.Now()); len(got) != 0 {
		t.Errorf("proposed a held directory: %+v", got)
	}
}
