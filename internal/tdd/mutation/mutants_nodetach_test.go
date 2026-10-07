package mutation

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Mutation runs at commit, in the foreground, and nowhere else: no hook,
// verb or sweep starts a detached process to measure, prepare or build
// anything for it. A detached build outlives the hook that started it, stacks
// with the next, and loads the box nobody asked to load; the owner's rule is
// that nothing is prepared before a commit asks for it. Read at the source, so
// it holds for a spawn written next year as for the ones deleted this year.

// repoGoSources is every non-test Go file of the module, by slash path
// relative to its root, with its text.
func repoGoSources(t *testing.T) map[string]string {
	t.Helper()
	// tree-read-ok: a policy test over the module's own sources; no fixture could stand in for what a future spawn looks like
	root := filepath.Join("..", "..", "..")
	out := map[string]string{}
	for _, top := range []string{"internal", "cmd", "tools"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("reading %s: %v", top, err)
		}
	}
	if len(out) < 100 {
		t.Fatalf("read only %d sources; the walk is not looking at the module", len(out))
	}
	return out
}

// The only places a detached process is started are the suite build the edit
// hook asks for and the session-end gc sweep. Neither is mutation.
var detachedSpawners = []string{
	"internal/tdd/gc/gc_session.go",
	"internal/tdd/postedit/deferred_run.go",
}

// isSpawnPlumbing is a generated or platform file that only names the detach
// primitive for the package that uses it.
func isSpawnPlumbing(path string) bool {
	return strings.HasSuffix(path, "/export.go") || strings.Contains(path, "/deps_") ||
		strings.HasSuffix(path, "_unix.go") || strings.HasSuffix(path, "_windows.go")
}

func TestDetachedProcess_NoSourceStartsOneForMutation(t *testing.T) {
	spawn := regexp.MustCompile(`\b[dD]etachedAttrs\(\)|\blaunchDetached\b`)
	mutationVerb := regexp.MustCompile(`"mutants"\s*,\s*"(?:testmap|edit|run|commit|prove|verdict|hold)"|"gate"\s*,\s*"mutants"`)
	for path, src := range repoGoSources(t) {
		if spawn.MatchString(src) {
			if !slices.Contains(detachedSpawners, path) && !isSpawnPlumbing(path) {
				t.Errorf("%s starts a detached process (%s); mutation never runs detached, and the only spawners are %v",
					path, spawn.FindString(src), detachedSpawners)
			}
			if mutationVerb.MatchString(src) {
				t.Errorf("%s starts a detached process and names a mutation verb (%s): nothing detached may build or run mutation",
					path, mutationVerb.FindString(src))
			}
		}
		// A process that runs the mutants verbs is started by the person or by
		// CI, never by this binary on its own account.
		if mutationVerb.MatchString(src) && !strings.HasPrefix(path, "internal/cli/") && !strings.HasPrefix(path, "internal/tdd/mutation/") &&
			(strings.Contains(src, "exec.Command") || strings.Contains(src, "proc.Spawn")) {
			t.Errorf("%s names a mutation verb and starts a process: mutation is run by hand, by CI or by the commit gate itself, never spawned", path)
		}
	}
}

// Every detached spawner is accounted for: if one of the two disappears or a
// third is added, this says so and the list above is edited on purpose.
func TestDetachedSpawners_AreExactlyTheKnownTwo(t *testing.T) {
	spawn := regexp.MustCompile(`\bdetachedAttrs\(\)|\blaunchDetached\b`)
	var found []string
	for path, src := range repoGoSources(t) {
		if spawn.MatchString(src) && !isSpawnPlumbing(path) {
			found = append(found, path)
		}
	}
	slices.Sort(found)
	if !slices.Equal(found, detachedSpawners) {
		t.Errorf("detached spawners = %v, want %v", found, detachedSpawners)
	}
}
