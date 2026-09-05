package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// findModuleRoot walks up from the test's own working directory to the
// directory holding go.mod, so the test finds THIS repo's CLAUDE.md
// regardless of which package directory `go test` runs it from.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test's working directory")
		}
		dir = parent
	}
}

// commandSurfaceSection returns the body of CLAUDE.md's "## Command surface"
// section (up to the next "## " heading), so a test judges only that prose
// and not, say, the Layout or Conventions sections.
func commandSurfaceSection(t *testing.T) string {
	t.Helper()
	root := findModuleRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	const heading = "## Command surface"
	i := strings.Index(string(data), heading)
	if i < 0 {
		t.Fatal(`CLAUDE.md has no "## Command surface" section`)
	}
	rest := string(data)[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// bulletVerbGroups finds the bullet line that starts "- `lead`" and returns
// every parenthesized, slash-joined, backtick-quoted verb list on it — e.g.
// "(`create`/`claim`/`unclaim`)" — in source order. This is the shape both
// the `workspace` and `gate` bullets use to name their live sub-verbs.
func bulletVerbGroups(section, lead string) [][]string {
	// A bullet's parenthesized verb list routinely wraps across a markdown
	// line, so the bullet itself is matched non-greedily up to the next line
	// that starts a new bullet (or the section's end), not just one line.
	bulletRe := regexp.MustCompile(`(?s)- ` + regexp.QuoteMeta("`"+lead+"`") + `.*?(?:\n- |\z)`)
	bullet := bulletRe.FindString(section)
	groupRe := regexp.MustCompile(`(?s)\((?:\s*` + "`[a-z][a-z-]*`" + `\s*/?)+\)`)
	verbRe := regexp.MustCompile("`([a-z][a-z-]*)`")
	var groups [][]string
	for _, g := range groupRe.FindAllString(bullet, -1) {
		var verbs []string
		for _, m := range verbRe.FindAllStringSubmatch(g, -1) {
			verbs = append(verbs, m[1])
		}
		groups = append(groups, verbs)
	}
	return groups
}

// TestClaudeMD_CommandSurfaceNamesOnlyLiveVerbs fails on today's CLAUDE.md,
// which names `prepare` and `cleanup` (worktree lifecycle) and `pr`/`ship`
// (git verbs) under the `workspace` bullet's parenthesized lists — none of
// which the dispatch table WorkspaceVerbs() recognizes as the CURRENT name
// for `prepare`/`cleanup` (they never existed), catching those two, while
// `pr`/`ship` are genuinely live and must stay once the stale ones are gone.
func TestClaudeMD_CommandSurfaceNamesOnlyLiveVerbs(t *testing.T) {
	section := commandSurfaceSection(t)
	cases := []struct {
		lead  string
		table func() []string
	}{
		{"workspace", WorkspaceVerbs},
		{"gate", GateVerbs},
	}
	for _, c := range cases {
		live := map[string]bool{}
		for _, v := range c.table() {
			live[v] = true
		}
		groups := bulletVerbGroups(section, c.lead)
		if len(groups) == 0 {
			t.Fatalf("no parenthesized verb group found on the %q bullet", c.lead)
		}
		for _, group := range groups {
			for _, verb := range group {
				if !live[verb] {
					t.Errorf("CLAUDE.md Command surface names %q under `%s`, which is not a live verb", verb, c.lead)
				}
			}
		}
	}
}

// TestClaudeMD_CommandSurfaceNamesEveryTopLevelVerb: each of TopLevelVerbs()
// appears in the section, as its own backtick-quoted token.
func TestClaudeMD_CommandSurfaceNamesEveryTopLevelVerb(t *testing.T) {
	section := commandSurfaceSection(t)
	for _, verb := range TopLevelVerbs() {
		re := regexp.MustCompile("`" + regexp.QuoteMeta(verb) + "[`\\s]")
		if !re.MatchString(section) {
			t.Errorf("CLAUDE.md Command surface never mentions top-level verb %q", verb)
		}
	}
}

// assertNeverUnknown fails if the dispatcher reported the verb as
// unrecognized — the one thing TestSurface_TablesMatchDispatch exists to
// catch: a table entry the real dispatch has no case for.
func assertNeverUnknown(t *testing.T, verb string, out, errb string) {
	t.Helper()
	combined := out + errb
	if strings.Contains(combined, "unknown command") || strings.Contains(combined, "unknown subcommand") {
		t.Errorf("dispatch reports %q as unknown\nstdout: %s\nstderr: %s", verb, out, errb)
	}
}

// TestSurface_TablesMatchDispatch proves every verb these tables list is one
// the real dispatch recognizes, so a table entry can never silently drift
// from what Run/runGate/runWorkspace actually handle. It runs from a bare,
// non-git temp directory: the git-hook verbs (postcommit, precommit,
// premerge/premergecommit, prepush) look up the current repo before doing
// any real work and return cleanly with none found, which this relies on to
// exercise their dispatch without touching real git state. `cargo` and
// `git` are gate's own pass-through shims to whatever real toolchain the
// box resolves — invoking them here would depend on host state a unit test
// must not depend on, so they are skipped; their own dedicated shim tests
// cover them.
func TestSurface_TablesMatchDispatch(t *testing.T) {
	t.Chdir(t.TempDir())
	// `posttooluse` flips this package-wide flag as a side effect of
	// dispatch; restore it so exercising the verb here can't change what a
	// later test in this binary observes.
	t.Cleanup(func() { tdd.EnableDeferredPhases(false) })

	for _, v := range TopLevelVerbs() {
		var out, errb bytes.Buffer
		Run([]string{v, "--help"}, strings.NewReader(""), &out, &errb)
		assertNeverUnknown(t, v, out.String(), errb.String())
	}

	for _, v := range GateVerbs() {
		if v == "cargo" || v == "git" {
			continue
		}
		var out, errb bytes.Buffer
		runGate([]string{v, "--help"}, strings.NewReader(""), &out, &errb)
		assertNeverUnknown(t, v, out.String(), errb.String())
	}

	for _, v := range WorkspaceVerbs() {
		var out, errb bytes.Buffer
		runWorkspace([]string{v, "--help"}, &out, &errb)
		assertNeverUnknown(t, v, out.String(), errb.String())
	}
}
