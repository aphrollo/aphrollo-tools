package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// noTargetDirEnv is resolveTargetDir's env func with CARGO_TARGET_DIR unset,
// so the config-file precedence under test actually gets a chance to answer.
func noTargetDirEnv(string) string { return "" }

// A repo configuring build.target-dir through its own .cargo/config.toml had
// its REAL target dir go unresolved: resolveTargetDir answered
// <ws>/target regardless, so the build-slot lock keyed on the wrong
// directory and the GC sweep proposed the real, live target dir as a stray
// once idle (issue #285).
func TestResolveTargetDir_HonoursProjectCargoConfigTargetDir(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"custom-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "custom-target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the project config's target-dir %q", got, want)
	}
}

// A legal, common line — `target-dir = "custom-target"  # shared cache` —
// used to leave TrimSpace looking at a value ending in the comment text, not
// the closing quote, so strings.Trim stripped only the leading quote and fed
// `custom-target"  # shared cache` to every lock and GC decision: a
// confident WRONG path, not the documented parse-miss fallback.
func TestResolveTargetDir_StripsInlineCommentFromProjectConfigTargetDir(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"custom-target\"  # shared cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "custom-target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the project config's target-dir %q (comment must not leak into the path)", got, want)
	}
}

// An absolute target-dir in the project config is used as-is, not joined to
// the workspace root.
func TestResolveTargetDir_HonoursAbsoluteProjectCargoConfigTargetDir(t *testing.T) {
	ws := t.TempDir()
	abs := filepath.Join(t.TempDir(), "elsewhere-target")
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[build]\ntarget-dir = \"" + filepath.ToSlash(abs) + "\"\n"
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveTargetDir(noTargetDirEnv, ws); got != filepath.Clean(abs) {
		t.Fatalf("resolveTargetDir = %q, want the absolute config path %q", got, abs)
	}
}

// CARGO_TARGET_DIR is cargo's own highest-precedence answer and must still
// win over a project config declaring a different target-dir.
func TestResolveTargetDir_EnvVarStillOverridesProjectConfig(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"custom-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envTarget := filepath.Join(t.TempDir(), "env-target")
	env := func(k string) string {
		if k == "CARGO_TARGET_DIR" {
			return envTarget
		}
		return ""
	}
	if got := resolveTargetDir(env, ws); got != filepath.Clean(envTarget) {
		t.Fatalf("resolveTargetDir = %q, want CARGO_TARGET_DIR %q to win over the project config", got, envTarget)
	}
}

// No project config at all, and no target-dir key in the [build] table that
// IS present, both fall back to the plain default unchanged.
func TestResolveTargetDir_FallsBackWhenProjectConfigDeclaresNoTargetDir(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"),
		[]byte("[build]\njobs = 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the plain default %q", got, want)
	}
}

// A build.target-dir declared one level ABOVE the workspace root -- a
// documented cargo mechanism and a normal monorepo/CI-mount layout where
// several checkouts share one build cache -- used to be invisible: the
// resolver checked only workspaceRoot itself, then jumped straight to
// $CARGO_HOME. Real cargo walks every ancestor directory, so this must be
// found before the user config is even consulted (issue #422).
func TestResolveTargetDir_HonoursAncestorCargoConfigTargetDir(t *testing.T) {
	parent := t.TempDir()
	ws := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(parent, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"shared-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "shared-target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the ancestor config's target-dir %q", got, want)
	}
}

// A workspace-root config and an ancestor config both declaring
// build.target-dir: cargo's hierarchical merge lets the level closer to the
// invocation directory win for a scalar key, so the workspace root's own
// answer must not be shadowed by the one above it.
func TestResolveTargetDir_WorkspaceCargoConfigWinsOverAncestor(t *testing.T) {
	parent := t.TempDir()
	ws := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(parent, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"shared-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".cargo", "config.toml"),
		[]byte("[build]\ntarget-dir = \"own-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "own-target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the workspace's own target-dir %q over the ancestor's", got, want)
	}
}

// With no project config at all, the USER cargo config's target-dir is the
// next answer cargo itself would give.
func TestResolveTargetDir_FallsBackToUserCargoConfigTargetDir(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("CARGO_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[build]\ntarget-dir = \"user-target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "user-target")
	if got := resolveTargetDir(noTargetDirEnv, ws); got != want {
		t.Fatalf("resolveTargetDir = %q, want the user config's target-dir %q", got, want)
	}
}
