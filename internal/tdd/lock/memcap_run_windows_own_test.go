//go:build windows

package lock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// runawaySource is a program that grows by 16 MB steps, touching every page,
// up to 600 MB, then waits (bounded) to be ended. It starts allocating the
// moment it runs. Held at the cap, the Go runtime retries its refused commit
// rather than dying at once, so the tests bound the child with a deadline.
const runawaySource = `package main

import "time"

func main() {
	var held [][]byte
	for done := 0; done < 600; done += 16 {
		b := make([]byte, 16<<20)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
		held = append(held, b)
	}
	<-time.After(60 * time.Second)
	_ = held[0][0]
}
`

// runawayDeadline is how long a child held at the cap is given before it is
// ended: a child that was not held has finished allocating by then.
const runawayDeadline = 5 * time.Second

// buildRunaway compiles runawaySource and answers the command that runs it,
// ended after runawayDeadline.
func buildRunaway(t *testing.T) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"main.go": runawaySource, "go.mod": "module runaway\n\ngo 1.26\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "runaway.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the runaway: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runawayDeadline)
	t.Cleanup(cancel)
	return exec.CommandContext(ctx, exe)
}

// The child allocates the moment it runs, so a limit applied after it has
// started finds its whole working set already committed and holds nothing. A
// child held from its first instruction stops growing at the cap, is still
// there at the deadline, and the result says the cap held it; one that was not
// held has finished its allocations and exits cleanly after its own wait.
func TestLaunchCapped_HoldsAChildThatAllocatesAtOnce(t *testing.T) {
	cmd := buildRunaway(t)

	res, err := launchCapped(cmd, MemCap{MB: 100, Why: "test"})

	if err == nil {
		t.Fatal("a child that outgrew the cap exited cleanly")
	}
	if res.Mode != "job" || !res.Killed || res.Kills != 1 {
		t.Fatalf("result = %+v, want the job enforcer to report the run ended at the cap", res)
	}
}

// A mutation tool's worker the cap ends is one worker's death: the run goes on.
func TestLaunchCapped_KillLargestReportsTheWorkerNotTheRun(t *testing.T) {
	cmd := buildRunaway(t)

	res, err := launchCapped(cmd, MemCap{MB: 100, Why: "test", KillLargest: true})

	if err == nil {
		t.Fatal("a child that outgrew the cap exited cleanly")
	}
	if res.Killed || res.Kills != 1 {
		t.Fatalf("result = %+v, want one kill and the run not ended", res)
	}
}
