package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGCLegacyJobs_ADirectoryThatDoesNotShowItIsOursSurvivesAndIsReported(t *testing.T) {
	jobs := t.TempDir()
	baseTarget := filepath.Join(jobs, "job-base")
	mkFile(t, filepath.Join(baseTarget, "tmp", "base-target", "a.o"), "x", 10*24*time.Hour)
	cargo := filepath.Join(jobs, "job-cargo")
	mkFile(t, filepath.Join(cargo, "target", "CACHEDIR.TAG"), "x", 10*24*time.Hour)
	if err := os.MkdirAll(filepath.Join(cargo, "target", "debug"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(jobs, "someone-elses")
	mkFile(t, filepath.Join(foreign, "notes.md"), "mine", 10*24*time.Hour)

	remove, left := legacyJobDirs(jobs, time.Now(), nil)

	if len(remove) != 2 || remove[0].Path != baseTarget || remove[1].Path != cargo {
		t.Fatalf("remove = %+v, want the tmp/base-target and the cargo target layouts", remove)
	}
	if len(left) != 1 || left[0] != foreign {
		t.Errorf("left alone = %v, want %s", left, foreign)
	}
}
