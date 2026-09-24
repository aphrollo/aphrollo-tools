package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// healthyInstall lays out a config dir and shim dir that every check passes,
// so each test below breaks exactly one thing and names the check that sees it.
func healthyInstall(t *testing.T) DoctorInput {
	t.Helper()
	// A healthy INSTALL, on a box with room: free space is a fact about the
	// machine rather than about the install, and a CI runner with 13 GB free
	// would otherwise fail this baseline for a warning that is correct.
	withFreeSpace(t, 200)
	cfg := t.TempDir()
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("APHROLLO"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSettings(cfg, bin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAgents(cfg); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "cargo-queue")
	if _, err := InstallCargoShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGitShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := installShimExes(shim, bin, doctorShimExeNames()); err != nil {
		t.Fatal(err)
	}
	// A managed hooks dir, built by writing the same shim install writes —
	// never through InitGitGate, which would touch the box's own real global
	// git config.
	hooksDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(binShim(bin, "precommit", "")), 0o755); err != nil {
		t.Fatal(err)
	}
	return DoctorInput{
		ConfigDir:    cfg,
		Bin:          bin,
		ShimDir:      shim,
		Repo:         t.TempDir(),
		PathDirs:     []string{shim, binDir},
		GitHooksPath: hooksDir,
	}
}

// check finds one check by name; the test fails if the name is not reported at
// all, because a check that silently disappears reports "healthy".
func check(t *testing.T, checks []DoctorCheck, name string) DoctorCheck {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %v", name, checkNames(checks))
	return DoctorCheck{}
}

func checkNames(checks []DoctorCheck) []string {
	var names []string
	for _, c := range checks {
		names = append(names, c.Name)
	}
	return names
}

// TestDoctor_HealthyInstallPassesEveryCheck is the baseline: with everything
// wired the way init wires it, doctor must report nothing. A doctor that
// complains about a correct install is one nobody runs.
func TestDoctor_HealthyInstallPassesEveryCheck(t *testing.T) {
	for _, c := range Doctor(healthyInstall(t)) {
		if !c.OK {
			t.Errorf("check %q failed on a healthy install: %s", c.Name, c.Detail)
		}
	}
}

// TestDoctor_SeesAnUnsetGitHooksPath is the failure that disarmed this box on
// 2026-09-05: nothing else can tell that git runs no hooks at all when
// core.hooksPath is unset, and every other check still reads "ok".
func TestDoctor_SeesAnUnsetGitHooksPath(t *testing.T) {
	in := healthyInstall(t)
	in.GitHooksPath = ""

	c := check(t, Doctor(in), "git hooks path")
	if c.OK {
		t.Fatal("an unset core.hooksPath must fail the check")
	}
}

// TestDoctor_SeesADanglingGitHooksPath catches the exact incident: the
// configured hooks dir has been deleted out from under core.hooksPath, so
// git silently runs nothing.
func TestDoctor_SeesADanglingGitHooksPath(t *testing.T) {
	in := healthyInstall(t)
	in.GitHooksPath = filepath.Join(t.TempDir(), "gone")

	c := check(t, Doctor(in), "git hooks path")
	if c.OK {
		t.Fatal("a core.hooksPath naming a missing directory must fail the check")
	}
	if !strings.Contains(c.Detail, "does not exist") {
		t.Fatalf("the failure must say the dir is missing, got: %s", c.Detail)
	}
}

// TestDoctor_SeesAForeignGitHooksPath catches core.hooksPath pointed at a
// real directory that carries no marker this tool wrote — hooks that exist
// but were never installed by aphrollo, or an empty leftover dir.
func TestDoctor_SeesAForeignGitHooksPath(t *testing.T) {
	in := healthyInstall(t)
	foreign := t.TempDir()
	in.GitHooksPath = foreign

	c := check(t, Doctor(in), "git hooks path")
	if c.OK {
		t.Fatal("a core.hooksPath dir with no managed shim must fail the check")
	}
}

// TestDoctor_SeesHooksSplitAcrossTwoBinaries catches the state that makes the
// gate behave differently between events: half the hooks pointing at an old
// copy of the binary after a move or a rename.
func TestDoctor_SeesHooksSplitAcrossTwoBinaries(t *testing.T) {
	in := healthyInstall(t)
	path := filepath.Join(in.ConfigDir, "settings.json")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.ToSlash(filepath.Join(t.TempDir(), "aphrollo-old.exe"))
	patched := strings.Replace(string(doc), filepath.ToSlash(in.Bin), other, 1)
	if patched == string(doc) {
		t.Fatal("setup: no hook command carried the bin path")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	c := check(t, Doctor(in), "hook binary")
	if c.OK {
		t.Fatal("two different hook binaries must fail the check")
	}
	if !strings.Contains(c.Detail, other) {
		t.Fatalf("the failure must name both paths, got: %s", c.Detail)
	}
}

// TestDoctor_SeesAStaleHookBinary catches the drift that produces a gate one
// build behind the binary a session actually runs.
func TestDoctor_SeesAStaleHookBinary(t *testing.T) {
	in := healthyInstall(t)
	stale := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(stale, []byte("OLDER-BUILD"), 0o755); err != nil {
		t.Fatal(err)
	}
	in.Bin = stale

	c := check(t, Doctor(in), "hook binary")
	if c.OK {
		t.Fatal("a hook binary that is not this executable must fail the check")
	}
	if !strings.Contains(c.Detail, stale) {
		t.Fatalf("the failure must name both paths, got: %s", c.Detail)
	}
}

// TestDoctor_SeesAGitOrCargoResolvingBeforeTheShimDir catches the setup where
// a direct `cargo`/`git` reaches the real toolchain and never queues: the
// criterion is not "the shim is literally first" (machine PATH on a real
// Windows box always starts with System32 and friends, ahead of anything a
// user configures — #293), it is that nothing ahead of the shim actually
// resolves either command.
//
// ratchet: test_removed TestDoctor_SeesTheShimDirNotFirstOnPath: asserted the
// literal-first criterion the comment above replaces; superseded by this test
// plus TestDoctor_AcceptsTheShimDirBehindHarmlessEntries and
// TestDoctor_SeesTheShimDirMissingFromPathEntirely.
func TestDoctor_SeesAGitOrCargoResolvingBeforeTheShimDir(t *testing.T) {
	in := healthyInstall(t)
	shadowing := t.TempDir()
	if err := os.WriteFile(filepath.Join(shadowing, realCommandExeName(t)), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	in.PathDirs = []string{shadowing, in.ShimDir}

	c := check(t, Doctor(in), "shim dir on PATH")
	if c.OK {
		t.Fatal("a real git/cargo executable ahead of the shim must fail the check")
	}
	if !strings.Contains(c.Detail, shadowing) {
		t.Fatalf("the failure must name the shadowing dir, got: %s", c.Detail)
	}
}

// TestDoctor_AcceptsTheShimDirBehindHarmlessEntries is #293's healthy case:
// on a real Windows box the machine-wide PATH (System32 and friends) is
// NEVER empty and always precedes a user's own configuration, so the shim
// is never literally PathDirs[0] on a correctly configured box. The check
// must pass as long as none of those earlier entries actually holds a git or
// cargo executable.
func TestDoctor_AcceptsTheShimDirBehindHarmlessEntries(t *testing.T) {
	in := healthyInstall(t)
	harmless := t.TempDir()
	if err := os.WriteFile(filepath.Join(harmless, "notepad.exe"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	in.PathDirs = []string{harmless, in.ShimDir}

	if c := check(t, Doctor(in), "shim dir on PATH"); !c.OK {
		t.Fatalf("an entry ahead of the shim with no git/cargo in it must not fail the check: %s", c.Detail)
	}
}

// TestDoctor_SeesTheShimDirMissingFromPathEntirely is the other failure
// shape: the shim never appears in PathDirs at all, not merely behind
// something — a box where the queue dir fell off PATH altogether.
func TestDoctor_SeesTheShimDirMissingFromPathEntirely(t *testing.T) {
	in := healthyInstall(t)
	in.PathDirs = []string{t.TempDir(), t.TempDir()}

	if c := check(t, Doctor(in), "shim dir on PATH"); c.OK {
		t.Fatal("a shim dir absent from PATH entirely must fail the check")
	}
}

// realCommandExeName is the filename that would actually shadow the shim on
// this OS: only Windows resolves by extension.
func realCommandExeName(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return "git.exe"
	}
	return "git"
}

// TestDoctor_SeesALeftoverBatchShim catches the shim that mangles arguments:
// cmd.exe strips `^`, so a `git rev-parse MERGE_HEAD^{tree}` through it is a
// wrong answer, not a slow one.
func TestDoctor_SeesALeftoverBatchShim(t *testing.T) {
	in := healthyInstall(t)
	if err := os.WriteFile(filepath.Join(in.ShimDir, "git.cmd"), []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := check(t, Doctor(in), "batch shims removed")
	if c.OK {
		t.Fatal("a leftover .cmd shim must fail the check")
	}
	if !strings.Contains(c.Detail, "git.cmd") {
		t.Fatalf("the failure must name the file, got: %s", c.Detail)
	}
}

// TestDoctor_SeesAShortenedHookTimeout catches the setting that makes the
// harness kill a hook before its own deadline fires: no verdict is returned,
// no state is stamped, and the spawned build is orphaned.
func TestDoctor_SeesAShortenedHookTimeout(t *testing.T) {
	in := healthyInstall(t)
	path := filepath.Join(in.ConfigDir, "settings.json")
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

	if c := check(t, Doctor(in), "hook timeouts"); c.OK {
		t.Fatal("a PostToolUse timeout below the post-edit budget must fail the check")
	}
}

// TestDoctor_SeesTheRetiredSlashCommand catches /tdd being defined twice: the
// skill carries the name now, and the leftover stub shadows it.
func TestDoctor_SeesTheRetiredSlashCommand(t *testing.T) {
	in := healthyInstall(t)
	cmd := filepath.Join(in.ConfigDir, "commands", "tdd.md")
	if err := os.MkdirAll(filepath.Dir(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmd, []byte("intercepted by aphrollo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if c := check(t, Doctor(in), "retired /tdd command"); c.OK {
		t.Fatal("a leftover commands/tdd.md must fail the check")
	}
}

// TestDoctor_SeesAnEditedManagedFile catches a skill or agent that no longer
// matches the binary's own template — a session then follows rules the gate
// does not enforce.
func TestDoctor_SeesAnEditedManagedFile(t *testing.T) {
	in := healthyInstall(t)
	edited := filepath.Join(in.ConfigDir, "agents", "reviewer.md")
	if err := os.WriteFile(edited, []byte("---\nname: reviewer\n---\n\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := check(t, Doctor(in), "managed skills and agents")
	if c.OK {
		t.Fatal("an edited managed file must fail the check")
	}
	if !strings.Contains(c.Detail, "reviewer") {
		t.Fatalf("the failure must name the file, got: %s", c.Detail)
	}
}

// TestDoctor_SeesAMissingManagedFile is the other half: a deleted skill is as
// bad as an edited one, and it is the state a partial uninstall leaves.
func TestDoctor_SeesAMissingManagedFile(t *testing.T) {
	in := healthyInstall(t)
	if err := os.Remove(filepath.Join(in.ConfigDir, "skills", "sdd", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	if c := check(t, Doctor(in), "managed skills and agents"); c.OK {
		t.Fatal("a missing managed skill must fail the check")
	}
}

// TestDoctor_WarnsWhenACargoWorkspaceHasNoCIFile keeps the CI check advisory:
// a workspace with no workflow is a choice, not a fault, and doctor must not
// exit 1 over it.
func TestDoctor_WarnsWhenACargoWorkspaceHasNoCIFile(t *testing.T) {
	in := healthyInstall(t)
	writeCargoWorkspace(t, in.Repo)

	c := check(t, Doctor(in), "CI clippy list")
	if !c.OK {
		t.Fatalf("a missing CI file must not fail: %s", c.Detail)
	}
	if !c.Warn {
		t.Fatal("a missing CI file must be reported as a warning")
	}
}

// TestDoctor_SeesACIClippyListThatIsNotDerived catches the list that drifts
// silently: crates named in the workflow instead of read from the workspace's
// own clippy-clean declaration, so adding one to the manifest gates nothing.
func TestDoctor_SeesACIClippyListThatIsNotDerived(t *testing.T) {
	in := healthyInstall(t)
	writeCargoWorkspace(t, in.Repo)
	writeWorkflow(t, in.Repo, "cargo clippy -p server -p shared -- -D warnings\n")

	c := check(t, Doctor(in), "CI clippy list")
	if c.OK {
		t.Fatal("a hand-written CI clippy list must fail the check")
	}
	if c.Warn {
		t.Fatal("a workflow that exists and does not derive the list is a failure, not a warning")
	}
}

// TestDoctor_AcceptsACIClippyListDerivedFromTheManifest is the passing shape:
// the workflow calls the script that reads clippy-clean.
func TestDoctor_AcceptsACIClippyListDerivedFromTheManifest(t *testing.T) {
	in := healthyInstall(t)
	writeCargoWorkspace(t, in.Repo)
	writeWorkflow(t, in.Repo, "run: tools/clippy_clean_list.sh | xargs -n1 cargo clippy -p\n")

	if c := check(t, Doctor(in), "CI clippy list"); !c.OK {
		t.Fatalf("a derived list must pass: %s", c.Detail)
	}
}

// TestDoctor_SkipsTheCIClippyCheckOutsideACargoWorkspace keeps the report
// about the tree it is standing in: a Go repo has no clippy-clean list.
func TestDoctor_SkipsTheCIClippyCheckOutsideACargoWorkspace(t *testing.T) {
	in := healthyInstall(t)
	for _, c := range Doctor(in) {
		if c.Name == "CI clippy list" {
			t.Fatal("the CI clippy check must not run outside a cargo workspace")
		}
	}
}

// TestRenderDoctor_ExitsOneOnAnyFailure pins the contract a script depends on:
// one line per check, and a non-zero exit only when something actually failed.
func TestRenderDoctor_ExitsOneOnAnyFailure(t *testing.T) {
	healthy := []DoctorCheck{{Name: "a", OK: true}, {Name: "b", OK: true, Warn: true, Detail: "no CI file"}}
	out, code := RenderDoctor(healthy)
	if code != 0 {
		t.Fatalf("exit = %d on checks that all passed, want 0\n%s", code, out)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("want one line per check, got %d:\n%s", n, out)
	}

	out, code = RenderDoctor(append(healthy, DoctorCheck{Name: "c", Detail: "run `aphrollo gate init`"}))
	if code != 1 {
		t.Fatalf("exit = %d with a failing check, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "run `aphrollo gate init`") {
		t.Fatalf("a failure must print FAIL and its fix, got:\n%s", out)
	}
}

func writeCargoWorkspace(t *testing.T, dir string) {
	t.Helper()
	body := "[workspace]\nmembers = [\"crates/*\"]\n\n[workspace.metadata.aphrollo]\nclippy-clean = [\"server\", \"shared\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeWorkflow(t *testing.T, dir, body string) {
	t.Helper()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
