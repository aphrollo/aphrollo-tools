package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_BuildFailureExitsNonzeroAndFilesNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-input", "testdata/buildfail.jsonl"}, nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (a build failure is infrastructure, not a flake)", code)
	}
	if !strings.Contains(stderr.String(), "fixturemod/pkgbuildfail") {
		t.Fatalf("stderr does not name the broken package: %q", stderr.String())
	}
}

func TestRun_AllPassingExitsZeroAndFilesNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-input", "testdata/allpass.jsonl"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "nothing to file") {
		t.Fatalf("stdout = %q, want it to say there was nothing to file", stdout.String())
	}
}

func TestRun_MissingInputFileExitsNonzero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-input", "testdata/does-not-exist.jsonl"}, nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for a missing input file", code)
	}
}
