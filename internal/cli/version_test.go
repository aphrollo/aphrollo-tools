package cli

import (
	"bytes"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// A binary built with -buildvcs=false otherwise has no way to say what it
// is; `aphrollo version` is the one place that answer is printed, so it
// must say so honestly rather than a misleadingly empty commit.

func TestVersion_PrintsUnstampedWithoutALinkerStamp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, want 0", code)
	}
	if got, want := out.String(), "aphrollo (unstamped)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

func TestVersion_PrintsShortShaAndBuildTime(t *testing.T) {
	buildinfo.SetForTest("ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2", "2026-09-05T02:57:00Z")
	t.Cleanup(func() { buildinfo.SetForTest("", "") })

	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, want 0", code)
	}
	if got, want := out.String(), "aphrollo ca47dba built 2026-09-05T02:57:00Z\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

// version takes no arguments at all: -h and anything else are both errors,
// same shape, so a caller who typos a flag gets a usage line instead of a
// silently-ignored argument.
func TestVersion_RejectsArguments(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"anything"}} {
		var out, errb bytes.Buffer
		if code := runVersion(args, &out, &errb); code != 2 {
			t.Fatalf("runVersion(%v) exit = %d, want 2", args, code)
		}
		if got, want := errb.String(), "usage: aphrollo version\n"; got != want {
			t.Fatalf("runVersion(%v) stderr = %q, want %q", args, got, want)
		}
	}
}
