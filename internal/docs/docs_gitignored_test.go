package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The escape that moved this guard off the working tree (borld#301) was a
// citation to a file .gitignore kept out of the commit, so "the file is
// ignored" is the shape everyone remembers and the wrong thing to test for.
// `git check-ignore` answers a question about the RULES; the index answers
// the question a citation actually makes a promise about. The two part
// company in a way that matters: a repo may ignore a whole extension and
// still track particular files under it — `git add -f` is ordinary, legal
// git, and this repo's own `.gitignore` is not a list of what the commit
// carries. A guard built on check-ignore would refuse those citations, which
// is a false refusal nobody can clear without deleting a line from
// .gitignore.

// TestCheck_ATrackedFileThatAlsoMatchesGitignoreStillResolves is that floor.
func TestCheck_ATrackedFileThatAlsoMatchesGitignoreStillResolves(t *testing.T) {
	root := gitRepo(t, map[string]string{
		".gitignore":    "*.md\n",
		"NOTES.md":      "see `docs/notes.md` for the rest\n",
		"docs/notes.md": "x\n",
	})
	gitAdd(t, root, "-f", ".gitignore", "NOTES.md", "docs/notes.md")
	var out strings.Builder
	failed, err := Check(root, nil, &out)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if failed {
		t.Fatalf("Check refused %q — docs/notes.md is gitignored AND tracked, so the commit carries it", out.String())
	}
}

// TestCheck_AnIgnoredUntrackedFileDoesNotResolve is the escape itself, end to
// end through git: the cited file is on this disk and in no commit, so no
// other checkout can follow the citation.
func TestCheck_AnIgnoredUntrackedFileDoesNotResolve(t *testing.T) {
	root := gitRepo(t, map[string]string{
		".gitignore":    "*.md\n",
		"NOTES.md":      "see `docs/notes.md` for the rest\n",
		"docs/notes.md": "x\n",
	})
	gitAdd(t, root, "-f", ".gitignore", "NOTES.md")
	var out strings.Builder
	failed, err := Check(root, nil, &out)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !failed || !strings.Contains(out.String(), "docs/notes.md") {
		t.Fatalf("Check passed %q — docs/notes.md is in no commit", out.String())
	}
}

// gitRepo lays out a repository with the given repo-relative files and no
// commit: `git ls-files` reads the INDEX, which is what these tests are about,
// and committing would run this box's global gate hooks over a fixture.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run(t, root, "init", "-q")
	for rel, body := range files {
		writeRepoFile(t, root, rel, body)
	}
	return root
}

func gitAdd(t *testing.T, root string, args ...string) {
	t.Helper()
	run(t, root, append([]string{"add"}, args...)...)
}

func run(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "no-such-gitconfig"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
