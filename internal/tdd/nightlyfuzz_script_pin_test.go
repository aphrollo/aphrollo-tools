package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzStepScript returns the body of the "Fuzz every target for 60s" step's
// `run: |` block from nightly-fuzz.yml, dedented to column 0.
func fuzzStepScript(t *testing.T) string {
	t.Helper()
	wf := repoFile(t, ".github", "workflows", "nightly-fuzz.yml")
	_, rest, ok := strings.Cut(wf, "- name: Fuzz every target for 60s")
	if !ok {
		t.Fatal("nightly-fuzz.yml has no fuzz step")
	}
	_, rest, ok = strings.Cut(rest, "        run: |\n")
	if !ok {
		t.Fatal("the fuzz step has no run block")
	}
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		if line != "" && !strings.HasPrefix(line, "          ") {
			break
		}
		out = append(out, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(out, "\n")
}

// TestNightlyFuzzWorkflow_AMissingCorpusDirIsNotAFailure runs the fuzz step
// under `bash -e` (the shell GitHub gives a `run:` block) with a fake `go`
// that passes, in a tree with no testdata/fuzz directory: a fresh checkout.
// The corpus snapshot must treat the absent directory as empty; the run died
// 3 ms after its first banner, before go ran, when `find` on it failed under
// pipefail.
func TestNightlyFuzzWorkflow_AMissingCorpusDirIsNotAFailure(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed") // skip-ok: the step is a bash script; a host without bash cannot run it
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(fuzzStepScript(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-e", filepath.ToSlash(script))
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PATH="+filepath.ToSlash(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the fuzz step failed with no corpus directory: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "FuzzLexer_AgreesWithTheLexerItReplaced: no crash") {
		t.Fatalf("the last target never reported a clean run:\n%s", out)
	}
}
