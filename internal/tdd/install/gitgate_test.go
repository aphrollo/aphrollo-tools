package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func isolateGitConfig(t *testing.T) string { t.Helper(); return tddtest.IsolateGitConfig(t) }

func globalHooksPath(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command(gitBinary(), "config", "--global", "--get", "core.hooksPath").Output()
	return strings.TrimSpace(string(out))
}

// Installing the git gate writes the two managed shims and points git's global
// core.hooksPath at the hooks dir, so every repo is gated by one command.
func TestInitGitGate_Installs(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true installing the git gate")
	}
	for name, sub := range map[string]string{"pre-commit": "precommit", "pre-merge-commit": "premerge"} {
		data, err := os.ReadFile(filepath.Join(hooksDir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if !strings.Contains(string(data), CmdName+" "+sub) {
			t.Errorf("%s does not invoke tdd %s:\n%s", name, sub, data)
		}
		// NTFS carries no exec bit (os.Stat reports 0666); git runs the shim
		// through sh on Windows regardless, so the bit only matters elsewhere.
		if fi, _ := os.Stat(filepath.Join(hooksDir, name)); runtime.GOOS != "windows" && fi != nil && fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", name)
		}
	}
	// The gate is mechanical-only now: pre-push is no longer a managed hook, so
	// install must NOT write a pre-push shim.
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-push")); !os.IsNotExist(err) {
		t.Errorf("pre-push shim should not be installed (gate is mechanical-only), stat err=%v", err)
	}
	if got := globalHooksPath(t); got != hooksDir {
		t.Errorf("core.hooksPath = %q, want %q", got, hooksDir)
	}
}

// The global git gate's pre-merge-commit shim must invoke the same renamed
// "premerge" subcommand the per-repo install already does (see
// TestInstall_WiresPreMergeCommitToGatePremerge) — one gate, one spelling,
// not the pre-rename "premergecommit" alias surviving in just this copy.
func TestInitGitGate_WiresPreMergeCommitToGatePremerge(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(hooksDir, "pre-merge-commit"))
	if err != nil {
		t.Fatalf("pre-merge-commit not written: %v", err)
	}
	if !strings.Contains(string(data), "gate premerge") {
		t.Fatalf("global pre-merge-commit shim does not invoke gate premerge:\n%s", data)
	}
	if strings.Contains(string(data), "premergecommit") {
		t.Fatalf("global pre-merge-commit shim still invokes the pre-rename spelling:\n%s", data)
	}
}

// A box installed before the mechanical-only change has a MANAGED pre-push shim
// in the hooks dir. The next install must prune that stranded managed shim so
// the lingering pre-push hook stops firing — while never touching a foreign
// (hand-written) pre-push hook.
func TestInitGitGate_PrunesStrandedManagedPrePush(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a previously-installed managed pre-push shim.
	managed := "#!/bin/sh\n" + installMarker + "\nexec /usr/local/bin/aphrollo tdd prepush \"$@\"\n"
	prePush := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(prePush, []byte(managed), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	if !changed {
		t.Error("expected changed=true (pruned the stranded pre-push shim)")
	}
	if _, err := os.Stat(prePush); !os.IsNotExist(err) {
		t.Errorf("stranded managed pre-push shim was not pruned, stat err=%v", err)
	}
}

// A FOREIGN (hand-written) pre-push hook must survive install: the prune only
// removes shims this tool wrote, never a user's own hook.
func TestInitGitGate_PreservesForeignPrePushOnPrune(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho my own pre-push\n"
	prePush := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(prePush, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	data, _ := os.ReadFile(prePush)
	if string(data) != foreign {
		t.Errorf("foreign pre-push was clobbered:\n%s", data)
	}
}

// The git gate install is idempotent.
func TestInitGitGate_Idempotent(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("first: %v", err)
	}
	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Error("expected changed=false on re-install")
	}
}

// A foreign (non-managed) hook is never overwritten.
func TestInitGitGate_PreservesForeignHook(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho mine\n"
	pc := filepath.Join(hooksDir, "pre-commit")
	if err := os.WriteFile(pc, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("InitGitGate: %v", err)
	}
	data, _ := os.ReadFile(pc)
	if string(data) != foreign {
		t.Errorf("foreign pre-commit was overwritten:\n%s", data)
	}
}

// A pre-existing FOREIGN global core.hooksPath must not be silently clobbered:
// the user has their own global hooks. Install refuses with an error and leaves
// the existing value intact, rather than overwriting a path it could never
// restore on uninstall.
func TestInitGitGate_RefusesForeignHooksPath(t *testing.T) {
	isolateGitConfig(t)
	foreignPath := filepath.Join(t.TempDir(), "their-hooks")
	// The directory must actually EXIST: a hooksPath naming a directory that
	// is simply gone is dangling, not foreign, and is reclaimed rather than
	// refused (see TestInstallGitGate_ReclaimsADanglingHooksPath).
	if err := os.MkdirAll(foreignPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gitBinary(), "config", "--global", "core.hooksPath", foreignPath).CombinedOutput(); err != nil {
		t.Fatalf("seed core.hooksPath: %v: %s", err, out)
	}

	hooksDir := filepath.Join(t.TempDir(), "hooks")
	_, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err == nil {
		t.Fatal("InitGitGate: want error refusing to clobber a foreign core.hooksPath, got nil")
	}
	if got := globalHooksPath(t); got != foreignPath {
		t.Errorf("foreign core.hooksPath was changed to %q, want preserved %q", got, foreignPath)
	}
}

// A hooks dir under the OS temp root is not a durable place for a
// MACHINE-WIDE core.hooksPath to live: whatever created it can delete it
// later, and every commit on the box then runs ungated with no error from
// git — this happened on this box (2026-09-05) via a --git-hooks-dir under a
// session scratchpad. Install must refuse rather than proceed.
func TestInstallGitGate_RefusesATempHooksDir(t *testing.T) {
	isolateGitConfig(t)
	t.Setenv(HooksDirUnsafeEnv, "") // this test proves the refusal WITHOUT the override
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	_, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err == nil {
		t.Fatal("InitGitGate: want error refusing a hooks dir under the temp root, got nil")
	}
	if got := globalHooksPath(t); got != "" {
		t.Errorf("core.hooksPath = %q, want unset after a refused install", got)
	}
}

// APHROLLO_HOOKS_DIR_UNSAFE=1 is the deliberate override a dogfood run needs
// (it never touches the box's real global config — isolateGitConfig points
// GIT_CONFIG_GLOBAL at a throwaway file for the whole test); set explicitly
// here even though isolateGitConfig already sets it, so this test still
// documents the override on its own.
func TestInstallGitGate_ForcedPastTheTempRefusalWithEnv(t *testing.T) {
	isolateGitConfig(t)
	t.Setenv(HooksDirUnsafeEnv, "1")
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate with %s=1: %v", HooksDirUnsafeEnv, err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
}

// unsafeHooksDirReason is the pure classifier the refusal above is built on;
// tested directly against fabricated paths so the scratchpad-segment branch
// is provable without creating a directory outside any temp root.
func TestUnsafeHooksDirReason_FlagsATempRoot(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "aphrollo-hooks")
	if reason := unsafeHooksDirReason(dir); reason == "" {
		t.Fatalf("unsafeHooksDirReason(%q) = \"\", want a reason naming the temp root", dir)
	}
}

func TestUnsafeHooksDirReason_FlagsAScratchpadSegment(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "home", "olive", "session-abc123", "scratchpad", "hooks")
	if reason := unsafeHooksDirReason(dir); reason == "" {
		t.Fatalf("unsafeHooksDirReason(%q) = \"\", want a reason naming the scratchpad segment", dir)
	}
}

func TestUnsafeHooksDirReason_AcceptsADurableDir(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "home", "olive", ".config", "git", "hooks")
	if reason := unsafeHooksDirReason(dir); reason != "" {
		t.Fatalf("unsafeHooksDirReason(%q) = %q, want \"\" for a durable dir", dir, reason)
	}
}

// A dangling core.hooksPath — the directory the box's global config still
// names has simply been deleted — is RECLAIMABLE, not foreign: nothing could
// still depend on hooks that cannot run. Install must repoint it instead of
// reading the missing dir as someone else's and refusing, which is what
// forced a manual `git config --global --unset core.hooksPath` on this box.
func TestInstallGitGate_ReclaimsADanglingHooksPath(t *testing.T) {
	isolateGitConfig(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	parent := t.TempDir()
	dangling := filepath.Join(parent, "gone")
	if out, err := exec.Command(gitBinary(), "config", "--global", "core.hooksPath", dangling).CombinedOutput(); err != nil {
		t.Fatalf("seed core.hooksPath: %v: %s", err, out)
	}
	// dangling itself is never created, but parent IS: this is what "cleaned
	// up later" looks like — confidently a deletion, not merely unreachable.

	hooksDir := filepath.Join(t.TempDir(), "hooks")
	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err != nil {
		t.Fatalf("InitGitGate: want a dangling hooksPath reclaimed, got error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true repointing a dangling core.hooksPath")
	}
	if got := globalHooksPath(t); got != hooksDir {
		t.Errorf("core.hooksPath = %q, want %q", got, hooksDir)
	}
	log := gateLogContent(t)
	if !strings.Contains(log, "hookspath-dangling-repaired") {
		t.Fatalf("the repair must be recorded through appendGateLog so it lands in gate.log and is countable, got:\n%s", log)
	}
	if !strings.Contains(log, dangling) {
		t.Fatalf("the log line must name the previous value so it can be restored by hand: %q not found in:\n%s", dangling, log)
	}
}

// A core.hooksPath naming something merely UNREACHABLE right now — an
// unmounted or not-yet-reconnected mapped network drive returns the exact
// same "not found" error on Windows a deleted directory does — must be
// refused, never silently repointed: the depender may be fine and the stat
// transiently wrong, and rewriting a machine-wide setting out from under
// that is strictly worse than the unconditional refusal it would replace.
// The distinguishing signal this test drives: dangling's own PARENT also
// does not exist (neither was ever created), which a genuinely deleted
// directory's parent would not exhibit.
func TestInstallGitGate_RefusesAnUnreachableHooksPathRatherThanReclaimingIt(t *testing.T) {
	isolateGitConfig(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	unreachable := filepath.Join(root, "never-mounted", "shared-hooks")
	if out, err := exec.Command(gitBinary(), "config", "--global", "core.hooksPath", unreachable).CombinedOutput(); err != nil {
		t.Fatalf("seed core.hooksPath: %v: %s", err, out)
	}
	// Neither unreachable nor its parent is ever created.

	hooksDir := filepath.Join(t.TempDir(), "hooks")
	_, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false)
	if err == nil {
		t.Fatal("InitGitGate: want error refusing an unreachable core.hooksPath rather than reclaiming it, got nil")
	}
	if got := globalHooksPath(t); got != unreachable {
		t.Errorf("unreachable core.hooksPath was changed to %q, want preserved %q", got, unreachable)
	}
	if log := gateLogContent(t); strings.Contains(log, "hookspath-dangling-repaired") {
		t.Fatalf("an unreachable path must never be logged as repaired:\n%s", log)
	}
}

// Uninstall removes the managed shims and unsets core.hooksPath when it points
// at our dir.
func TestInitGitGate_Uninstall(t *testing.T) {
	isolateGitConfig(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if _, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	changed, err := InitGitGate(hooksDir, "/usr/local/bin/aphrollo", true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on uninstall")
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-commit")); !os.IsNotExist(err) {
		t.Error("pre-commit shim survived uninstall")
	}
	if got := globalHooksPath(t); got != "" {
		t.Errorf("core.hooksPath still set to %q after uninstall", got)
	}
}

// TestGitGate_InstallsTheCommitMsgHook pins the last mile: a gate that exists
// only as a subcommand never runs. git looks for a `commit-msg` hook by that
// exact name, and it must receive the message path git passes it.
func TestGitGate_InstallsTheCommitMsgHook(t *testing.T) {
	found := false
	for _, h := range gitGateHooks {
		if h.Name == "commit-msg" {
			found = true
			if h.Sub != "commitmsg" {
				t.Fatalf("commit-msg shim calls %q, want commitmsg", h.Sub)
			}
		}
	}
	if !found {
		t.Fatal("the global gate must install a commit-msg hook")
	}
	if !strings.Contains(binShim("/bin/aphrollo", "commitmsg", ""), `"$@"`) {
		t.Fatal("the shim must forward git's arguments — the message path is one of them")
	}

	perRepo := false
	for _, h := range perRepoHooks {
		if h.Name == "commit-msg" {
			perRepo = true
		}
	}
	if !perRepo {
		t.Fatal("per-repo install must write the commit-msg hook too")
	}
	if !strings.Contains(shim("/bin/aphrollo", "commitmsg"), `"$@"`) {
		t.Fatal("the per-repo shim must forward git's arguments too")
	}
}
