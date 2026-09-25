package tdd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// `git ls-remote https://...` spawns a git-remote-https helper that inherits
// the stdout pipe cmd.Output() wires up, and git itself stays alive waiting
// on that helper. This fixture is a two-role stand-in for that shape: role
// "spawn" (the git stub) starts a copy of itself as "hold" with Stdout wired
// to its OWN os.Stdout — the same fd cmd.Output() gave it — then blocks on
// Wait for it, exactly as git blocks on the helper. "hold" never touches the
// fd itself; it just sleeps long enough that only a kill, not an ordinary
// exit, closes it. A Cancel that reaches only the direct child (git/"spawn")
// kills something still alive but leaves "hold" as an orphan still holding
// the pipe; a Cancel that reaches the whole tree kills both.
const lsRemoteHangFixtureSource = `package main

import (
	"os"
	"os/exec"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		time.Sleep(30 * time.Second)
		return
	}
	child := exec.Command(os.Args[0], "hold")
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	_ = child.Wait()
}
`

// lsRemoteHangFixtureBinary builds that fixture once for the whole package.
var lsRemoteHangFixtureBinary = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-lsremote-hang-fixture")
	if err != nil {
		return "", err
	}
	tddtest.RegisterTempDir(dir)
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(lsRemoteHangFixtureSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module lsremotehangfixture\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	name := "lsremotehang"
	if runtime.GOOS == "windows" {
		name = "lsremotehang.exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the ls-remote hang fixture: %v\n%s", err, out)
	}
	return bin, nil
})

// A session start that hangs past its own budget because a helper process it
// never directly owns kept a pipe open is exactly the defect this feature
// exists to prevent — proving runLsRemote itself returns promptly matters
// independently of the higher-level cache/timeout wiring in BinaryBehindLine.
func TestRunLsRemote_ReturnsWithinTheBudgetWhenTheHelperHangs(t *testing.T) {
	// Deliberately not t.Parallel(): the assertion below is a real wall-clock
	// budget on process spawn/kill/wait, not a logic check. Marked parallel it
	// measured 1.691s against the old 1.5s ceiling under -shuffle=on (this
	// package's other newly-parallel tests compete for the same OS
	// process-table/CPU budget); serial, the same fixture stayed inside 1.5s.
	// A real defect in runLsRemote itself would still show serially — this is
	// a resource-contention hazard specific to this test's own timing
	// assertion. Left unmarked, and the ceiling raised to 5s (#543): a
	// measured 1.691s against a 1.5s bar is a flake waiting to happen, and
	// the ceiling is not what gives this test its teeth. The regression it
	// guards is the fixture's 30s hang leaking past the 300ms context — 5s
	// still fails that by a factor of six, while leaving room for process
	// spawn and kill on a box under concurrent builds. The 300ms context
	// budget below is the tight number and is deliberately untouched.
	bin, err := lsRemoteHangFixtureBinary()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = runLsRemote(ctx, bin)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runLsRemote succeeded against a helper that never answers, want an error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("runLsRemote took %s to return (budget 300ms + a bounded wind-down), want at most 5s — the hang this guards is 30s", elapsed)
	}
}
