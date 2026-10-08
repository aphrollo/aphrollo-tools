package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shadowDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, realCommandExeName(t)), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The PATH an agent shell gets is the one inherited by this process, with
// the queue dir first. The registry's order, with Git ahead of the queue dir,
// is not what that shell resolves against, so it must not fail the check.
func TestDoctor_JudgesThePathThisProcessHasNotTheRegistryOrder(t *testing.T) {
	in := healthyInstall(t)
	shadowing := shadowDir(t)
	in.PathDirs = []string{shadowing, in.ShimDir}
	in.ProcessPathDirs = []string{in.ShimDir, shadowing}

	if c := check(t, Doctor(in), "shim dir on PATH"); !c.OK {
		t.Fatalf("the process PATH has the queue dir first, yet the check failed: %s", c.Detail)
	}
}

// A real git ahead of the queue dir on the PATH this process has is still a
// miss: a direct git in this shell never queues.
func TestDoctor_SeesAShadowOnThePathThisProcessHas(t *testing.T) {
	in := healthyInstall(t)
	shadowing := shadowDir(t)
	in.PathDirs = []string{in.ShimDir}
	in.ProcessPathDirs = []string{shadowing, in.ShimDir}

	c := check(t, Doctor(in), "shim dir on PATH")
	if c.OK || !strings.Contains(c.Detail, shadowing) {
		t.Fatalf("a shadow on the process PATH must fail and name its dir: %+v", c)
	}
}

// The registry order is reported as information, never as a miss: a
// person's own terminal is only warned.
func TestDoctor_ReportsTheRegistryOrderAsInfoNotAMiss(t *testing.T) {
	in := healthyInstall(t)
	shadowing := shadowDir(t)
	in.PathDirs = []string{shadowing, in.ShimDir}
	in.ProcessPathDirs = []string{in.ShimDir, shadowing}

	c := check(t, Doctor(in), "shim dir on system PATH")
	if !c.OK || !c.Info {
		t.Fatalf("the registry order must be an ok info line, got %+v", c)
	}
	if !strings.Contains(c.Detail, shadowing) {
		t.Errorf("the info line must name the dir that precedes the queue dir: %s", c.Detail)
	}
	out, code := RenderDoctor([]DoctorCheck{c})
	if code != 0 || !strings.HasPrefix(out, "info  ") {
		t.Errorf("RenderDoctor = (%q, %d), want an info line and exit 0", out, code)
	}
}

// A registry PATH with the queue dir ahead of every shadow has nothing to add.
func TestDoctor_NoSystemPathLineWhenTheRegistryOrderIsFine(t *testing.T) {
	in := healthyInstall(t)
	in.PathDirs = []string{in.ShimDir, shadowDir(t)}
	in.ProcessPathDirs = []string{in.ShimDir}

	for _, c := range Doctor(in) {
		if c.Name == "shim dir on system PATH" {
			t.Fatalf("unexpected line for a healthy registry order: %+v", c)
		}
	}
}
