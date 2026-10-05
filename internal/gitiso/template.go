package gitiso

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A test that wants a committed repo used to run `git init`, two `git config`s,
// an add and a commit for it: six spawns, ~65 ms each on Windows, hundreds of
// times per package. A package builds its common repo once, from TestMain,
// with BuildRepo, and each test takes a copy with CopyRepo — a repo in its own
// right, with its own objects, index and HEAD.

// queuedMarker is the variable the git queue shim reads as "run straight
// through"; a fixture's own git has no business queueing behind the box.
const queuedMarker = "APHROLLO_GIT_QUEUED=1"

// BuildRepo makes dir a git repo on main with an identity and one commit
// holding files (path relative to dir -> content), or no commit at all when
// there are none. Call it from TestMain,
// after Isolate, so the git it runs sees no operator config.
func BuildRepo(dir string, files map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	git := func(args ...string) error {
		// exec-ok: gitiso cannot import internal/run: run's own tests run under gitiso.Main, so the import would be a cycle in test.
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = buildEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"config", "core.autocrlf", "false"},
	} {
		if err := git(args...); err != nil {
			return err
		}
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if len(files) == 0 {
		return nil
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	return git("commit", "-q", "-m", "init")
}

// buildEnv is the environment a template's git runs under: the caller's own
// with the variables that point git at a repository or a config removed, so
// the build is the same whichever test first asks for the template.
func buildEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, queuedMarker, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
}

// CopyRepo copies a repo BuildRepo made into dst. It never overwrites: a
// destination already holding one of the template's files is a copy handed out
// twice.
func CopyRepo(dst, src string) error {
	return os.CopyFS(dst, os.DirFS(src))
}
