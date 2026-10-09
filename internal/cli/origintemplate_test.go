package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// A fixture that needs a bare origin with a seed checkout and a clone of it
// spent a dozen git spawns on building them, in every test that asked. Each
// shape is built once per run, from the first test that asks, and each test
// copies it: an origin, a seed and a clone of its own, with the seed's and the
// clone's remote rewritten to the copy's origin.

// trioPlaceholder stands in a template's remote urls until a copy names its
// own origin.
const trioPlaceholder = "ORIGIN-URL-PLACEHOLDER"

// gitTrio is a bare origin, the seed checkout that pushed to it and, when the
// shape has one, a clone of it ("" otherwise).
type gitTrio struct{ origin, seed, clone string }

var trioTemplates sync.Map

// trioTemplate is the trio key names, built by build the first time any test
// asks. build gets the template's root and a runner for git in a directory; its
// work repos name the origin by the url it made them with, and trioTemplate puts
// the placeholder in their place.
func trioTemplate(key string, build func(root string, git func(dir string, args ...string)) gitTrio) gitTrio {
	once, _ := trioTemplates.LoadOrStore(key, sync.OnceValue(func() gitTrio {
		root, err := os.MkdirTemp("", "aphrollo-cli-trio-template-")
		if err != nil {
			panic(err)
		}
		registerStubDir(root)
		git := func(dir string, args ...string) {
			// exec-ok: a fixture build, run once per package under TestMain's isolation.
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			for _, kv := range os.Environ() {
				if name, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(name, "GIT_") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
			if out, err := cmd.CombinedOutput(); err != nil {
				panic("trio template: git " + strings.Join(args, " ") + ": " + err.Error() + "\n" + string(out))
			}
		}
		tmpl := build(root, git)
		for _, work := range []string{tmpl.seed, tmpl.clone} {
			if work != "" {
				git(work, "config", "remote.origin.url", trioPlaceholder)
			}
		}
		return tmpl
	}))
	return once.(func() gitTrio)()
}

// copyTrio copies a template into the three directories, which are empty or
// absent, and points the copies' remotes at the copy's origin. A template with
// no clone leaves clone alone.
func copyTrio(t *testing.T, tmpl gitTrio, origin, seed, clone string) {
	t.Helper()
	for _, c := range [][2]string{{origin, tmpl.origin}, {seed, tmpl.seed}, {clone, tmpl.clone}} {
		if c[1] == "" {
			continue
		}
		if err := gitiso.CopyRepo(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
		cfg := filepath.Join(c[0], ".git", "config")
		if c[1] == tmpl.origin {
			continue
		}
		data, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.ReplaceAll(string(data), trioPlaceholder, filepath.ToSlash(origin)))
		if err := os.WriteFile(cfg, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// updateTrio is the shape updateFixture hands out: a bare origin on main, the
// seed checkout that pushed main and the tag v99.0.0 to it, and a clone.
func updateTrio() gitTrio {
	return trioTemplate("update", func(root string, git func(dir string, args ...string)) gitTrio {
		origin, seed, clone := filepath.Join(root, "origin"), filepath.Join(root, "seed"), filepath.Join(root, "clone")
		git(root, "init", "-q", "--bare", "-b", "main", origin)
		git(root, "init", "-q", seed)
		git(seed, "config", "user.email", "t@example.com")
		git(seed, "config", "user.name", "t")
		git(seed, "checkout", "-q", "-B", "main")
		writeTrioFile(seed, "go.mod", "module github.com/aphrollo/aphrollo-tools\n\ngo 1.26.6\n")
		git(seed, "add", "-A")
		git(seed, "commit", "-q", "-m", "init")
		git(seed, "remote", "add", "origin", origin)
		git(seed, "tag", "v99.0.0")
		git(seed, "push", "-q", "origin", "main", "v99.0.0")
		git(root, "clone", "-q", origin, clone)
		git(clone, "config", "user.email", "t@example.com")
		git(clone, "config", "user.name", "t")
		return gitTrio{origin, seed, clone}
	})
}

// staleBranchTrio is the shape staleBranchOrigin hands out: a bare origin on
// main and a seed clone of it carrying one pushed file.
func staleBranchTrio() gitTrio {
	return trioTemplate("stale-branch", func(root string, git func(dir string, args ...string)) gitTrio {
		origin, seed := filepath.Join(root, "origin"), filepath.Join(root, "seed")
		git(root, "init", "-q", "--bare", "-b", "main", origin)
		git(root, "clone", "-q", origin, seed)
		git(seed, "config", "user.email", "t@example.com")
		git(seed, "config", "user.name", "t")
		writeTrioFile(seed, "base.go", "package base\n")
		git(seed, "add", "-A")
		git(seed, "commit", "-q", "-m", "init")
		git(seed, "push", "-q", "origin", "main")
		return gitTrio{origin: origin, seed: seed}
	})
}

func writeTrioFile(dir, name, content string) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		panic(err)
	}
}

// Each copy of a trio is an origin, a seed and a clone of its own: a commit
// pushed from one copy never reaches another's origin, and each one's remotes
// name its own origin.
func TestTrioCopies_TrackTheirOwnOriginAndShareNothing(t *testing.T) {
	isolateGitConfigCLI(t)
	var origins, seeds, clones [2]string
	for i := range origins {
		origins[i], seeds[i], clones[i] = filepath.Join(t.TempDir(), "origin"), t.TempDir(), filepath.Join(t.TempDir(), "clone")
		copyTrio(t, updateTrio(), origins[i], seeds[i], clones[i])
	}
	for i := range origins {
		for _, work := range []string{seeds[i], clones[i]} {
			if got, want := gitOutput(t, "git", work, "remote", "get-url", "origin"), filepath.ToSlash(origins[i]); got != want {
				t.Errorf("origin url of %s = %q, want %q", work, got, want)
			}
		}
	}
	mustWriteFile(t, filepath.Join(seeds[0], "only-in-0.txt"), "0\n")
	gitOutput(t, "git", seeds[0], "add", "-A")
	gitOutput(t, "git", seeds[0], "commit", "-q", "-m", "only in 0")
	gitOutput(t, "git", seeds[0], "push", "-q", "origin", "main")
	if got := gitOutput(t, "git", origins[0], "log", "-1", "--format=%s", "main"); got != "only in 0" {
		t.Errorf("origin 0 tip = %q, want the pushed commit", got)
	}
	if got := gitOutput(t, "git", origins[1], "log", "-1", "--format=%s", "main"); got != "init" {
		t.Errorf("origin 1 tip = %q, want init: it took the other copy's push", got)
	}
	if got := gitOutput(t, "git", clones[1], "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/main" {
		t.Errorf("clone upstream = %q, want origin/main", got)
	}
	if tag := gitOutput(t, "git", origins[1], "tag", "--list", "v99.0.0"); tag != "v99.0.0" {
		t.Errorf("tags of origin 1 = %q, want v99.0.0", tag)
	}
}
