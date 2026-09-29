package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A repo in another language rescopes the common presets at init: the
// written law judges the globs it was given, and records the param so a later
// re-render reproduces it. A law whose scope needs more than source code keeps
// that part.
func TestRatchetInit_SourceIncludeRescopesTheCommonPresets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "dev_instruments.md"), "")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "init", "--repo", root, "--preset", "common",
		"--param", `pattern=^func (Test[A-Za-z0-9_]+)\(`, "--param", "prefixes=APP",
		"--param", `include="**/*_test.go"`,
		"--param", `source_include="**/*.py", "**/*.ts", "**/*.svelte"`}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	moduleSize := readFile(t, filepath.Join(root, ".ratchet", "laws", "module_size.toml"))
	if !strings.Contains(moduleSize, `include = ["**/*.py", "**/*.ts", "**/*.svelte"]`) {
		t.Errorf("module_size scope not rescoped:\n%s", moduleSize)
	}
	if !strings.Contains(moduleSize, `source_include = "\"**/*.py\", \"**/*.ts\", \"**/*.svelte\""`) {
		t.Errorf("module_size must record the param it was rendered with:\n%s", moduleSize)
	}
	transient := readFile(t, filepath.Join(root, ".ratchet", "laws", "transient_doc_reference.toml"))
	if !strings.Contains(transient, `include = ["**/*.md", "**/*.py", "**/*.ts", "**/*.svelte"]`) {
		t.Errorf("transient_doc_reference must keep its markdown glob:\n%s", transient)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("check exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// Left unset, source_include is today's scope, and nothing records it.
func TestRatchetInit_UnsetSourceIncludeKeepsTodaysScope(t *testing.T) {
	root := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "init", "--repo", root, "--preset", "common",
		"--param", `pattern=^func (Test[A-Za-z0-9_]+)\(`, "--param", "prefixes=APP",
		"--param", `include="**/*_test.go"`}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	moduleSize := readFile(t, filepath.Join(root, ".ratchet", "laws", "module_size.toml"))
	if !strings.Contains(moduleSize, `include = ["**/*.rs", "**/*.go"]`) {
		t.Errorf("module_size lost its default scope:\n%s", moduleSize)
	}
	if strings.Contains(moduleSize, "source_include") || strings.Contains(moduleSize, "[params]") {
		t.Errorf("an unset param must not be recorded:\n%s", moduleSize)
	}
}

// The listing names required params first, then each optional one with the
// default it renders when left unset.
func TestRatchetPresets_ListsAnOptionalParamWithItsDefault(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "presets"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"common/module_size  params: source_include [default \"**/*.rs\", \"**/*.go\"]\n",
		"common/comment_hygiene  params: pattern, source_include [default \"**/*.rs\", \"**/*.go\", \"**/*.py\", \"**/*.ts\"]\n",
		"common/test_removed  params: include, pattern\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing missing %q:\n%s", want, got)
		}
	}
}
