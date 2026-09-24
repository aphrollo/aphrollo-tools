package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestDoctor_NeverTeachesARetiringAlias proves every fix `gate doctor` names
// points at the canonical spelling, never a verb the binary's own --help
// marks as a retiring alias (#551): doctor's whole job is telling an operator
// what to run next, so a dead spelling in its report names a command that no
// longer exists once the alias retires.
//
// Both sides come from the SAME source topLevelVerbTable/gateVerbTable that
// TestClaudeMDBlock_NeverTeachesARetiringAlias already reads from — nothing
// here hardcodes a verb name.
func TestDoctor_NeverTeachesARetiringAlias(t *testing.T) {
	report := allBrokenDoctorReports(t)
	checkTable(t, report, topLevelVerbTable, "aphrollo ")
	checkTable(t, report, gateVerbTable, "gate ")
}

// allBrokenDoctorReports renders doctor's report across a battery of broken
// installs, each breaking one field away from a healthy baseline (mirroring
// internal/tdd/install/doctor_test.go's own fixtures, built here through exported
// seams since internal/cli cannot reach that package's unexported test
// helpers) — concatenated so every FAIL message that names a command is
// exercised at least once.
func allBrokenDoctorReports(t *testing.T) string {
	t.Helper()
	base := healthyDoctorInput(t)
	var all strings.Builder
	// render runs the full check set for one broken input and appends its
	// report; the exit code is irrelevant here, only the printed text is.
	render := func(in tdd.DoctorInput) {
		out, _ := tdd.RenderDoctor(tdd.Doctor(in))
		all.WriteString(out)
	}

	// git hooks path: unset, dangling, foreign.
	unset := base
	unset.GitHooksPath = ""
	render(unset)

	dangling := base
	dangling.GitHooksPath = filepath.Join(t.TempDir(), "gone")
	render(dangling)

	foreign := base
	foreign.GitHooksPath = t.TempDir()
	render(foreign)

	// A fresh, empty config dir and shim dir: no settings.json (hook binary,
	// hook timeouts), no shim exes, no managed skills/agents.
	empty := base
	empty.ConfigDir = t.TempDir()
	empty.ShimDir = t.TempDir()
	render(empty)

	// hook binary: two distinct binaries in the same settings.json.
	twoBins := base
	twoBins.ConfigDir = t.TempDir()
	writeTwoHookBinaries(t, twoBins.ConfigDir, base.Bin)
	render(twoBins)

	// hook binary: the installed path in settings.json does not exist.
	missingBin := base
	missingBin.ConfigDir = t.TempDir()
	if _, err := tdd.InitSettings(missingBin.ConfigDir, filepath.Join(t.TempDir(), "gone.exe"), false); err != nil {
		t.Fatal(err)
	}
	render(missingBin)

	// hook binary: installed and current disagree — a stale build.
	staleBin := base
	staleBin.ConfigDir = t.TempDir()
	if _, err := tdd.InitSettings(staleBin.ConfigDir, base.Bin, false); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(stale, []byte("OLDER-BUILD"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleBin.Bin = stale
	render(staleBin)

	// hook timeouts: a managed hook's timeout below the budget init writes.
	shortTimeout := base
	shortTimeout.ConfigDir = t.TempDir()
	writeShortHookTimeout(t, shortTimeout.ConfigDir, base.Bin)
	render(shortTimeout)

	// leftover batch shim.
	batchShim := base
	batchShim.ShimDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(batchShim.ShimDir, "git.cmd"), []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	render(batchShim)

	// leftover /tdd command stub.
	retiredCmd := base
	retiredCmd.ConfigDir = t.TempDir()
	cmdPath := filepath.Join(retiredCmd.ConfigDir, "commands", "tdd.md")
	if err := os.MkdirAll(filepath.Dir(cmdPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmdPath, []byte("intercepted by aphrollo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	render(retiredCmd)

	return all.String()
}

// healthyDoctorInput lays out a config dir and shim dir that every check
// passes, so each fixture above breaks exactly one thing.
func healthyDoctorInput(t *testing.T) tdd.DoctorInput {
	t.Helper()
	cfg := t.TempDir()
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.InitSettings(cfg, bin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteTDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteSDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteAgents(cfg); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "cargo-queue")
	if _, err := tdd.InstallCargoShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.InstallGitShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.InstallShimExes(shim, bin); err != nil {
		t.Fatal(err)
	}
	hooksDir := t.TempDir()
	if err := tdd.WriteManagedHookForTest(hooksDir, "pre-commit", bin, "precommit"); err != nil {
		t.Fatal(err)
	}
	return tdd.DoctorInput{
		ConfigDir:    cfg,
		Bin:          bin,
		ShimDir:      shim,
		Repo:         t.TempDir(),
		PathDirs:     []string{shim, binDir},
		GitHooksPath: hooksDir,
	}
}

// writeTwoHookBinaries writes settings.json into cfg pointing at bin, then
// patches one managed hook command to name a different binary entirely — the
// state doctorHookBinary sees as hooks split across two builds.
func writeTwoHookBinaries(t *testing.T, cfg, bin string) {
	t.Helper()
	if _, err := tdd.InitSettings(cfg, bin, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "settings.json")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.ToSlash(filepath.Join(filepath.Dir(cfg), "aphrollo-old.exe"))
	patched := strings.Replace(string(doc), filepath.ToSlash(bin), other, 1)
	if patched == string(doc) {
		t.Fatal("setup: no hook command carried the bin path")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeShortHookTimeout writes settings.json into cfg pointing at bin, then
// lowers one PostToolUse hook's timeout below the budget init writes.
func writeShortHookTimeout(t *testing.T, cfg, bin string) {
	t.Helper()
	if _, err := tdd.InitSettings(cfg, bin, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "settings.json")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	group := hooks["PostToolUse"].([]any)[0].(map[string]any)
	group["hooks"].([]any)[0].(map[string]any)["timeout"] = 5
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
