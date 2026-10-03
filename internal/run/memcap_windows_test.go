//go:build windows

package run

import (
	"bytes"
	"context"
	"testing"
)

// The job's memory limit refuses an allocation past the cap, so a runaway
// child dies of its own failed allocation and the box keeps its memory. The
// Linux twin is the systemd scope or RSS watchdog, not built in this lane.
func TestHeavy_MemoryCapRefusesAnAllocationPastIt(t *testing.T) {
	allocate := func(mb string) (string, error) {
		var out bytes.Buffer
		spec := helperSpec(t, "alloc", mb)
		spec.MemoryMB, spec.Stdout = 256, &out
		c, err := StartHeavy(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		err = c.Wait()
		return out.String(), err
	}

	if out, err := allocate("32"); err != nil || out != "allocated 32\n" {
		t.Fatalf("32 MB under a 256 MB cap: output %q, err %v; want it allowed", out, err)
	}
	if out, err := allocate("1024"); err == nil || out != "" {
		t.Fatalf("1024 MB under a 256 MB cap: output %q, err %v; want it refused", out, err)
	}
}
