package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// A fixture that needs a repo with a bare origin used to spend a dozen git
// spawns on building one, in dozens of tests. The pair is built once per run,
// from the first test that asks, and each test copies it: a work repo and an
// origin of its own, with the work repo's remote rewritten to the copy's origin.

// originPlaceholder stands in the template's remote url until a copy names its
// own origin.
const originPlaceholder = "ORIGIN-URL-PLACEHOLDER"

// originPair is a built work repo and the bare origin it tracks.
type originPair struct{ work, origin string }

// originTemplateGit runs git for a template build: git itself, with no config
// of the box and no repository variable of the caller's, whichever test asks
// first.
func originTemplateGit(dir string, args ...string) string {
	// exec-ok: a fixture build, run once per package under TestMain's isolation.
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(name, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic("origin template: git " + strings.Join(args, " ") + ": " + err.Error() + "\n" + string(out))
	}
	return strings.TrimSpace(string(out))
}

// fixtureGitEnv gives the test the git world a throwaway repo needs: none of the
// hook's repository variables, and a global config of its own that names no
// hooks.
func fixtureGitEnv(t *testing.T) {
	t.Helper()
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

func originTemplateRoot() string {
	root, err := os.MkdirTemp("", "aphrollo-ws-origin-template-")
	if err != nil {
		panic(err)
	}
	registerStubDir(root)
	return root
}

// remoteTemplate is the repo repoWithRemote hands out: the committed repo with
// a bare origin, main pushed and tracked.
var remoteTemplate = sync.OnceValue(func() originPair {
	root := originTemplateRoot()
	work, origin := filepath.Join(root, "work"), filepath.Join(root, "origin.git")
	if err := gitiso.BuildRepo(work, map[string]string{"go.mod": "module x\n\ngo 1.26\n"}); err != nil {
		panic(err)
	}
	originTemplateGit(root, "init", "--bare", "-q", origin)
	originTemplateGit(work, "remote", "add", "origin", origin)
	originTemplateGit(work, "push", "-q", "-u", "origin", "main")
	originTemplateGit(work, "config", "remote.origin.url", originPlaceholder)
	return originPair{work, origin}
})

// cloneTemplate is the clone repoWithOrigin hands out: a clone of a bare origin
// holding one commit, both on main.
var cloneTemplate = sync.OnceValue(func() originPair {
	root := originTemplateRoot()
	seed, origin, clone := filepath.Join(root, "seed"), filepath.Join(root, "origin.git"), filepath.Join(root, "clone")
	if err := gitiso.BuildRepo(seed, map[string]string{"base.txt": "base\n"}); err != nil {
		panic(err)
	}
	originTemplateGit(root, "init", "--bare", "-q", "-b", "main", origin)
	originTemplateGit(seed, "remote", "add", "origin", origin)
	originTemplateGit(seed, "push", "-q", "origin", "main")
	originTemplateGit(root, "clone", "-q", origin, clone)
	originTemplateGit(clone, "config", "user.email", "t@t")
	originTemplateGit(clone, "config", "user.name", "t")
	originTemplateGit(clone, "config", "remote.origin.url", originPlaceholder)
	return originPair{clone, origin}
})

// copyOriginPair copies a template into workDst and originDst, both existing
// and empty or absent, and points the work repo's origin at the copy.
func copyOriginPair(t *testing.T, tmpl originPair, workDst, originDst string) {
	t.Helper()
	if err := copyPair(tmpl, workDst, originDst); err != nil {
		t.Fatal(err)
	}
}

func copyPair(tmpl originPair, workDst, originDst string) error {
	for _, c := range [][2]string{{workDst, tmpl.work}, {originDst, tmpl.origin}} {
		if err := gitiso.CopyRepo(c[0], c[1]); err != nil {
			return err
		}
	}
	cfg := filepath.Join(workDst, ".git", "config")
	data, err := os.ReadFile(cfg)
	if err != nil {
		return err
	}
	data = []byte(strings.ReplaceAll(string(data), originPlaceholder, filepath.ToSlash(originDst)))
	return os.WriteFile(cfg, data, 0o644)
}

// derivedPairs are the templates built on top of another one, by key: a test
// file's own setup, run once per package run and copied like the first two.
var derivedPairs sync.Map

// derivedPair is the pair key names, built by build on a copy of base the first
// time any test asks. build runs git through originTemplateGit and leaves the
// work repo's origin pointing at the copy's own origin; derivedPair puts the
// placeholder back.
func derivedPair(key string, base func() originPair, build func(work string)) originPair {
	once, _ := derivedPairs.LoadOrStore(key, sync.OnceValue(func() originPair {
		root := originTemplateRoot()
		work, origin := filepath.Join(root, "work"), filepath.Join(root, "origin.git")
		if err := copyPair(base(), work, origin); err != nil {
			panic(err)
		}
		build(work)
		originTemplateGit(work, "config", "remote.origin.url", originPlaceholder)
		return originPair{work, origin}
	}))
	return once.(func() originPair)()
}

// Each copy of a pair is a repo and an origin of its own: a commit pushed from
// one never reaches the other's origin, and each one's origin url names its own.
func TestOriginPair_CopiesTrackTheirOwnOriginAndShareNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		make func(*testing.T) string
	}{{"remote", repoWithRemote}, {"clone", repoWithOrigin}} {
		t.Run(c.name, func(t *testing.T) {
			a, b := c.make(t), c.make(t)
			urlA := gitOut(t, a, "remote", "get-url", "origin")
			urlB := gitOut(t, b, "remote", "get-url", "origin")
			if urlA == urlB || strings.Contains(urlA, originPlaceholder) || strings.Contains(urlB, originPlaceholder) {
				t.Fatalf("origin urls = %q and %q, want two distinct real paths", urlA, urlB)
			}
			writeFile(t, a, "only-in-a.txt", "a\n")
			gitRun(t, a, "add", ".")
			gitRun(t, a, "commit", "-q", "-m", "only in a")
			gitRun(t, a, "push", "-q", "origin", "main")
			if got := gitOut(t, urlA, "log", "-1", "--format=%s", "main"); got != "only in a" {
				t.Errorf("a's origin tip = %q, want the pushed commit", got)
			}
			if got := gitOut(t, urlB, "log", "-1", "--format=%s", "main"); got == "only in a" {
				t.Errorf("b's origin took a's push: %q", got)
			}
			if got := gitOut(t, b, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/main" {
				t.Errorf("b's upstream = %q, want origin/main", got)
			}
			if id := gitOut(t, b, "config", "user.email"); id != "t@t" {
				t.Errorf("b's identity = %q, want the fixture's t@t", id)
			}
		})
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// writeTemplateFile writes one file of a template's work repo, making its
// directory first.
func writeTemplateFile(work, rel, content string) {
	path := filepath.Join(work, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}
