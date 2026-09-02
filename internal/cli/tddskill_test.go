package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The procedure the gate assumes has to be reachable from the box the gate is
// installed on, so `gate init` writes it as a user-level skill — and takes the
// slash-command stub it replaces away with it. `--uninstall` puts the config
// dir back.
func TestGateInitWritesTheTDDSkillAndRetiresTheCommandStub(t *testing.T) {
	isolateGit(t)
	// init patches the CWD repo's CLAUDE.md; run from a dir that is no repo
	// so the suite never edits this one.
	elsewhere := t.TempDir()
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(before) })

	cfg := t.TempDir()
	stub := filepath.Join(cfg, "commands", "tdd.md")
	writeFile(t, stub, "intercepted by `aphrollo gate userpromptsubmit`\n")

	hooks := filepath.Join(t.TempDir(), "githooks")
	shims := filepath.Join(t.TempDir(), "bin", "cargo-queue")
	args := []string{"gate", "init", "--config-dir", cfg, "--bin", "/usr/local/bin/aphrollo",
		"--git-hooks-dir", hooks, "--cargo-shim-dir", shims}

	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "CLAUDE.md")); err == nil {
		t.Fatal("init wrote a CLAUDE.md into the working dir")
	}
	skill := filepath.Join(cfg, "skills", "tdd", "SKILL.md")
	body := readFile(t, skill)
	if !strings.Contains(body, "name: tdd") {
		t.Fatalf("skill body = %q", body)
	}
	if !strings.Contains(out.String(), "tdd skill") {
		t.Errorf("init must say it wrote the skill: %q", out.String())
	}
	if _, err := os.Stat(stub); !os.IsNotExist(err) {
		t.Errorf("the /tdd command stub must be retired by the skill: %v", err)
	}

	out.Reset()
	errb.Reset()
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("second init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if second := readFile(t, skill); second != body {
		t.Error("a second init rewrote the skill — it must be byte-identical")
	}

	out.Reset()
	errb.Reset()
	if code := Run(append(args, "--uninstall"), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("uninstall left the skill behind: %v", err)
	}
}
