package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates the package's run from the operator's machine.
//
// A REFUSING gh goes in front of the real one: this package shells out for
// `gh pr create`, `gh pr merge`, `gh pr edit` and `gh api`, and nothing but
// each test's own arrangement stood between a mutated guard and a real pull
// request being merged. The same exposure in internal/tdd filed three real
// issues against this repo from nobody (#155, #196, #197). See ghnet_test.go.
//
// HOME is redirected for the same class of reason: the prepare step runs
// `git config --global --add safe.directory <dir>`, which was writing a real
// person's ~/.gitconfig on every run. On the shared self-hosted runner two
// concurrent jobs wrote that one file at once and both died with
// `Apply: step 2 (mark git-safe) failed: exit status 255`, on a tip whose
// local gate had run the same suite green. See home_isolation_test.go.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-workspace-pkgtest-")
	if err != nil {
		panic(err)
	}
	redirectHome(dir)
	leaveTheBoxQueue()
	ghRefusalPath = installRefusingGh()
	code := m.Run()
	if ghRefusalPath != "" {
		os.RemoveAll(ghRefusalPath)
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// realHomeAtStart is the operator's own home, recorded before it is replaced,
// so a test can prove the replacement happened.
var realHomeAtStart string

// redirectHome points HOME (and the Windows and XDG spellings of it) at a
// throwaway directory, after pinning the Go caches so moving HOME cannot move
// them too.
func redirectHome(dir string) {
	if home, err := os.UserHomeDir(); err == nil {
		realHomeAtStart = home
	}
	pinGoEnv()
	fake := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fake, ".config"), 0o755); err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(k, fake); err != nil {
			panic(err)
		}
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(fake, ".config")); err != nil {
		panic(err)
	}
}

// pinGoEnv writes the toolchain's resolved cache locations into the
// environment, so redirecting HOME cannot move them: a suite that rebuilt
// every dependency from scratch because its cache moved is a suite nobody
// waits for.
func pinGoEnv() {
	names := []string{"GOPATH", "GOCACHE", "GOMODCACHE"}
	out, err := exec.Command("go", "env", strings.Join(names, " ")).Output()
	if err != nil {
		out, err = exec.Command("go", "env", names[0], names[1], names[2]).Output()
	}
	if err != nil {
		return
	}
	values := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n"), "\n")
	for i, name := range names {
		if i < len(values) && strings.TrimSpace(values[i]) != "" {
			_ = os.Setenv(name, strings.TrimSpace(values[i]))
		}
	}
}
