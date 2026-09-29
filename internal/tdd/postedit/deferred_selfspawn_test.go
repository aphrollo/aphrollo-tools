package postedit

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// spawnPhase started os.Executable() as `<exe> gate runphase --job ...`. When
// that was a Go test binary the child ran the whole suite instead (#997).
func TestSpawnPhase_RefusesAGoTestBinaryBeforeSavingAnything(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := selfExeFn
	selfExeFn = func() (string, error) { return filepath.Join(t.TempDir(), "go-build1", "b001", "tdd.test"), nil }
	t.Cleanup(func() { selfExeFn = prev })
	dir := t.TempDir()
	job := DeferredJob{Session: "s-997", Project: dir, Phase: "build", Dir: dir, Runner: []string{"true"}}

	got, ok := spawnPhase(job)

	if ok {
		t.Fatalf("spawnPhase started a phase from a Go test binary: %+v", got)
	}
	if _, saved := loadDeferredJob("s-997", dir); saved {
		t.Fatal("a refused spawn still saved the job record")
	}
}

func TestSpawnPhase_RefusesAChainAtTheDepthCap(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(proc.SpawnDepthEnv, "2")
	prev := selfExeFn
	selfExeFn = func() (string, error) { return filepath.Join(t.TempDir(), "aphrollo"), nil }
	t.Cleanup(func() { selfExeFn = prev })
	dir := t.TempDir()

	_, ok := spawnPhase(DeferredJob{Session: "s-997b", Project: dir, Phase: "build", Dir: dir, Runner: []string{"true"}})

	if ok {
		t.Fatal("spawnPhase started a third generation of self-spawn")
	}
	if _, saved := loadDeferredJob("s-997b", dir); saved {
		t.Fatal("a refused spawn still saved the job record")
	}
}
