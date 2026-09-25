//go:build !windows

package lock

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

// TestMeminfoGB_ReadsTheRealKeyInWholeGigabytes exercises meminfoGB against
// this box's actual /proc/meminfo (the path is not a seam — it is hardcoded,
// deliberately, so what this reads IS what every other consumer reads). The
// bar is what the function's own contract promises, not a box-specific
// number: MemTotal converts from kB to whole GB by integer division, so it
// must be an EXACT match against the same conversion applied to the raw kB
// figure this test reads itself, on this Linux CI box.
func TestMeminfoGB_ReadsTheRealKeyInWholeGigabytes(t *testing.T) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatalf("setup: this test needs a real /proc/meminfo (Linux CI): %v", err)
	}
	wantKB := -1
	for line := range splitLinesOwn(string(data)) {
		key, val, ok := cutColonOwn(line)
		if !ok || key != "MemTotal" {
			continue
		}
		fields := fieldsOwn(val)
		if len(fields) == 0 {
			t.Fatal("setup: MemTotal line has no value field")
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("setup: MemTotal value %q is not an integer: %v", fields[0], err)
		}
		wantKB = n
	}
	if wantKB < 0 {
		t.Fatal("setup: no MemTotal key in /proc/meminfo on this box")
	}

	if got, want := meminfoGB("MemTotal"), wantKB/(1<<20); got != want {
		t.Fatalf("meminfoGB(\"MemTotal\") = %d, want %d (the same kB->GB conversion applied to this box's own reading)", got, want)
	}
}

// TestMeminfoGB_UnknownKeyIsZero is the unknown-is-unknown rule: a key never
// found in the file (an old kernel missing MemAvailable, or a typo) must
// report 0, which every caller treats as "could not be read" rather than as
// a real zero-byte reading.
func TestMeminfoGB_UnknownKeyIsZero(t *testing.T) {
	if got := meminfoGB("NotARealMeminfoKey"); got != 0 {
		t.Fatalf("meminfoGB(\"NotARealMeminfoKey\") = %d, want 0", got)
	}
}

// TestMachineRAMGBAndMachineAvailGB_AgreeWithMeminfoGB pins both thin
// readers as exactly meminfoGB under their own key — MemTotal for the
// physical figure, MemAvailable for what a new process could actually get —
// rather than two independent derivations that could silently drift apart.
func TestMachineRAMGBAndMachineAvailGB_AgreeWithMeminfoGB(t *testing.T) {
	if got, want := machineRAMGB(), meminfoGB("MemTotal"); got != want {
		t.Fatalf("machineRAMGB() = %d, want meminfoGB(\"MemTotal\") = %d", got, want)
	}
	if got, want := machineAvailGB(), meminfoGB("MemAvailable"); got != want {
		t.Fatalf("machineAvailGB() = %d, want meminfoGB(\"MemAvailable\") = %d", got, want)
	}
}

// TestFreeSpaceGB_ReportsPositiveSpaceOnARealTempDir pins the happy path:
// t.TempDir() sits on a real, writable filesystem, so ok must be true and the
// reported figure must match the function's own contract — Bavail*Bsize
// converted to whole GiB — not some fixed floor. A box under real disk
// pressure legitimately has under 1 GiB free (measured: this box's shared
// tmpfs hit 96% full and freeSpaceGB genuinely read 0), so the earlier
// "strictly positive" assertion failed on that state without a bug in the
// function; asserting the CONTRACT instead — an independent syscall.Statfs
// read on the same path, put through the identical conversion — passes
// whatever the box's real free space is, while still catching a mutant that
// breaks the computation (wrong field, wrong shift, wrong operand order).
func TestFreeSpaceGB_ReportsPositiveSpaceOnARealTempDir(t *testing.T) {
	dir := t.TempDir()

	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("setup: syscall.Statfs(%q): %v", dir, err)
	}
	want := int(st.Bavail * uint64(st.Bsize) / (1 << 30))

	gb, ok := freeSpaceGB(dir)
	if !ok {
		t.Fatal("ok = false on a real temp dir, want true")
	}
	if gb != want {
		t.Fatalf("freeSpaceGB(%q) = %d, want %d (Bavail=%d * Bsize=%d / GiB, independently read)", dir, gb, want, st.Bavail, st.Bsize)
	}
}

// TestFreeSpaceGB_MissingPathIsNotOK is the failure half: a path statfs
// cannot resolve must report ok=false, not a fabricated 0-but-valid answer.
func TestFreeSpaceGB_MissingPathIsNotOK(t *testing.T) {
	if _, ok := freeSpaceGB("/this/path/does/not/exist/at/all/aphrollo-own-test"); ok {
		t.Fatal("ok = true for a nonexistent path, want false")
	}
}

// TestProcessExePath_ResolvesThisTestBinarysOwnLiveImage pins the seam this
// exists for (catching a renamed-but-still-running installer image): this
// process's own pid always has a live /proc/<pid>/exe the kernel keeps
// pointed at the running image, so the call must succeed and return EXACTLY
// what os.Executable() independently resolves for this same running
// process — a wrong or merely non-empty path must fail this, not just an
// empty one.
func TestProcessExePath_ResolvesThisTestBinarysOwnLiveImage(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Fatalf("setup: os.Executable(): %v", err)
	}

	path, ok := processExePath(os.Getpid())
	if !ok {
		t.Fatal("ok = false reading this process's own /proc/<pid>/exe, want true")
	}
	if path != want {
		t.Fatalf("processExePath(self) = %q, want the independently resolved %q", path, want)
	}
}

// TestProcessExePath_UnknownPidIsNotOK is the failure half: a pid with no
// live process (and so no /proc/<pid>/exe symlink) must report ok=false, not
// a stale or fabricated path.
func TestProcessExePath_UnknownPidIsNotOK(t *testing.T) {
	// A pid this large cannot be a running process on any OS this box runs
	// on — the same fictitious-but-real pid other lock package tests already
	// use against the real OS liveness check.
	if _, ok := processExePath(0x7FFFFFF0); ok {
		t.Fatal("ok = true for a pid with no live process, want false")
	}
}

// splitLinesOwn, cutColonOwn and fieldsOwn re-derive exactly the parsing
// meminfoGB itself does, kept local (rather than exported from production
// code) so the test's "want" is computed independently of the function
// under test's control flow while still reading the same real file the same
// way.
func splitLinesOwn(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := 0
		for i := 0; i < len(s); i++ {
			if s[i] == '\n' {
				if !yield(s[start:i]) {
					return
				}
				start = i + 1
			}
		}
		if start < len(s) {
			yield(s[start:])
		}
	}
}

func cutColonOwn(line string) (key, val string, ok bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			return line[:i], line[i+1:], true
		}
	}
	return "", "", false
}

func fieldsOwn(s string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(s); i++ {
		if i < len(s) && s[i] != ' ' && s[i] != '\t' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	return out
}
