package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test suite the gate starts runs in a scratch directory of its own, and its
// git is sealed to it: no directory the tests make can find a repository by
// walking up, the global config is a neutral one there, and nothing a hook
// exported (GIT_DIR, GIT_INDEX_FILE) reaches it (#1043).
func TestSuiteEnv_GoCargoAndLintRunnersAreSealedToTheirScratchDir(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/op/.gitconfig")
	dir := makeGoRepo(t)
	scratch := GoTmpRootDir(dir)
	if scratch == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\"", dir)
	}

	for _, cmd := range []string{"go", "cargo", "golangci-lint"} {
		env := suiteEnv(Runner{Cmd: cmd}, dir)

		if got := lastEnvValue(env, "GIT_CEILING_DIRECTORIES"); !strings.Contains(got, scratch) {
			t.Errorf("%s: GIT_CEILING_DIRECTORIES = %q, want it to name the scratch dir %q", cmd, got, scratch)
		}
		if got, want := lastEnvValue(env, "GIT_CONFIG_GLOBAL"), filepath.Join(scratch, "gitconfig"); got != want {
			t.Errorf("%s: GIT_CONFIG_GLOBAL = %q, want %q", cmd, got, want)
		}
		if got := lastEnvValue(env, "GIT_CONFIG_NOSYSTEM"); got != "1" {
			t.Errorf("%s: GIT_CONFIG_NOSYSTEM = %q, want 1", cmd, got)
		}
		if got := lastEnvValue(env, "GIT_DIR"); got != "" {
			t.Errorf("%s: GIT_DIR = %q, want it dropped", cmd, got)
		}
	}
	if data, err := os.ReadFile(filepath.Join(scratch, "gitconfig")); err != nil || !strings.Contains(string(data), "aphrollo-test") {
		t.Errorf("the sealed global config = %q, %v; want the neutral identity", data, err)
	}
}

// A runner with no scratch dir of its own is not sealed: there is no private
// directory to seal it to. Its git variables are only scrubbed, as before.
func TestSuiteEnv_ARunnerWithNoScratchDirIsNotSealed(t *testing.T) {
	dir := makeGoRepo(t)

	env := suiteEnv(Runner{Cmd: "pytest", Args: []string{"-q"}}, dir)

	for _, name := range []string{"GIT_CEILING_DIRECTORIES", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"} {
		if got := lastEnvValue(env, name); got != "" {
			t.Errorf("%s = %q for pytest, want it unset", name, got)
		}
	}
}

// Outside a checkout a go run has no scratch dir either, so there is nothing to
// seal it to.
func TestSuiteEnv_AGoRunnerOutsideACheckoutIsNotSealed(t *testing.T) {
	dir := t.TempDir()
	if GoTmpRootDir(dir) != "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = %q, want none outside a checkout", dir, GoTmpRootDir(dir))
	}

	env := suiteEnv(Runner{Cmd: "go", Args: []string{"test"}}, dir)

	if got := lastEnvValue(env, "GIT_CEILING_DIRECTORIES"); got != "" {
		t.Errorf("GIT_CEILING_DIRECTORIES = %q, want it unset", got)
	}
}
