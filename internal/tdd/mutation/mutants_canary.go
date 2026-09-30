package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The canary. A mutation runner starts test processes, and a test that reaches
// the real repository (through a GIT_DIR a hook exported, or a working directory
// inside the checkout) or the operator's global git config changes them
// silently: a post-merge test-map build once rewrote the primary's config,
// moved main through 127 fixture commits and switched worktree HEADs (#1043).
// Prevention is the sealed environment and the disposable copy; this is the
// detection behind it. Before a runner starts, the git state a leak would touch
// is fingerprinted, and after it ends it is fingerprinted again: any difference
// refuses the runner's result, says what changed, and records an escape.

// gitWorldPart is one thing the canary watches: a file's content or a git
// answer, and whether it was there at all.
type gitWorldPart struct {
	Label   string
	Present bool
	Text    string
}

// gitWorld is everything the canary watches, in a fixed order.
type gitWorld []gitWorldPart

// snapshotGitWorld reads what a test process reaching the real git would
// change: the config, the HEAD of the checkout at root and of the repository it
// belongs to, packed-refs, the branch list and the checked-out commit of that
// repository, and the operator's global git config. Outside a repository only
// the global config is read.
func snapshotGitWorld(root string) gitWorld {
	var w gitWorld
	if lane := RepoRoot(root); lane != "" {
		gitDir := strings.TrimSpace(gitOut(lane, "rev-parse", "--absolute-git-dir"))
		common := strings.TrimSpace(gitOut(lane, "rev-parse", "--path-format=absolute", "--git-common-dir"))
		if common != "" {
			w = append(w, filePart("the repository's config", filepath.Join(common, "config")))
			w = append(w, filePart("the repository's HEAD", filepath.Join(common, "HEAD")))
			w = append(w, filePart("packed-refs", filepath.Join(common, "packed-refs")))
		}
		if gitDir != "" {
			w = append(w, filePart("this checkout's HEAD", filepath.Join(gitDir, "HEAD")))
		}
		w = append(w, gitWorldPart{"the branches", true, gitOut(lane, "for-each-ref", "--format=%(refname)", "refs/heads")})
		w = append(w, gitWorldPart{"the checked-out commit", true, strings.TrimSpace(gitOut(lane, "rev-parse", "HEAD"))})
	}
	for _, path := range globalGitConfigs() {
		w = append(w, filePart("the global git config "+path, path))
	}
	return w
}

// filePart reads one file into a part; a file that is not there is a part that
// is not present, which differs from an empty one.
func filePart(label, path string) gitWorldPart {
	data, err := os.ReadFile(path)
	return gitWorldPart{Label: label, Present: err == nil, Text: string(data)}
}

// globalGitConfigs is the files git reads as the global config for this
// process: the one GIT_CONFIG_GLOBAL names, or else the XDG one and ~/.gitconfig.
func globalGitConfigs() []string {
	if path := os.Getenv("GIT_CONFIG_GLOBAL"); path != "" {
		return []string{path}
	}
	var paths []string
	home, _ := os.UserHomeDir()
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	} else if home != "" {
		paths = append(paths, filepath.Join(home, ".config", "git", "config"))
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	}
	return paths
}

// changesTo names each part of w that differs in after, with the lines it
// gained and lost.
func (w gitWorld) changesTo(after gitWorld) []string {
	var changes []string
	for i, was := range w {
		if i >= len(after) {
			break
		}
		now := after[i]
		if was.Label != now.Label || (was.Present == now.Present && was.Text == now.Text) {
			continue
		}
		change := was.Label + " changed"
		switch {
		case !was.Present:
			change += " (it was not there)"
		case !now.Present:
			change += " (it is gone)"
		}
		if diff := lineDiff(was.Text, now.Text); diff != "" {
			change += ":\n" + diff
		}
		changes = append(changes, change)
	}
	return changes
}

// diffLineCap is how many changed lines a report names before it stops.
const diffLineCap = 8

// lineDiff lists the lines after lost from before, then the ones it gained,
// each prefixed - or +, up to diffLineCap lines and then a count of the rest.
func lineDiff(before, after string) string {
	had, has := lineSet(before), lineSet(after)
	var out []string
	for _, line := range strings.Split(before, "\n") {
		if line != "" && !has[line] {
			out = append(out, "-"+line)
		}
	}
	for _, line := range strings.Split(after, "\n") {
		if line != "" && !had[line] {
			out = append(out, "+"+line)
		}
	}
	if len(out) > diffLineCap {
		rest := len(out) - diffLineCap
		out = append(out[:diffLineCap], fmt.Sprintf("…%d more", rest))
	}
	return strings.Join(out, "\n")
}

// lineSet is the non-empty lines of text.
func lineSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			set[line] = true
		}
	}
	return set
}

// gitWorldRecorder records an escape for a leak. It is nil until a package above
// this one, which may import the escape recorder, installs one.
var gitWorldRecorder func(root, runner, evidence string, log io.Writer)

// SetGitWorldRecorder installs the function that records an escape for a leak
// and answers the restore.
func SetGitWorldRecorder(fn func(root, runner, evidence string, log io.Writer)) (restore func()) {
	prev := gitWorldRecorder
	gitWorldRecorder = fn
	return func() { gitWorldRecorder = prev }
}

// gitWorldWatch is a runner's canary: the world as it stood when the runner
// started.
type gitWorldWatch struct {
	root, runner string
	before       gitWorld
}

// watchGitWorld takes the fingerprint runner's result is judged against.
func watchGitWorld(root, runner string) gitWorldWatch {
	return gitWorldWatch{root: root, runner: runner, before: snapshotGitWorld(root)}
}

// verify fingerprints the world again and answers what changed. A change is
// printed to log and recorded as an escape; the caller refuses its result.
func (g gitWorldWatch) verify(log io.Writer) []string {
	changes := g.before.changesTo(snapshotGitWorld(g.root))
	if len(changes) == 0 {
		return nil
	}
	logf(log, "%s", gitWorldRefusal(g.runner, g.root, changes))
	NoteGitWorldChange(g.root, g.runner, strings.Join(changes, "\n"), log)
	return changes
}

// NoteGitWorldChange records the escape for a leak through the recorder a
// package above installed, and does nothing when none is installed.
func NoteGitWorldChange(root, runner, evidence string, log io.Writer) {
	if gitWorldRecorder != nil {
		gitWorldRecorder(root, runner, evidence, log)
	}
}

// gitWorldRefusal is the message a refused result carries.
func gitWorldRefusal(runner, root string, changes []string) string {
	return fmt.Sprintf("mutants: the %s changed the git state of %s or the global git config while it ran, so its result is refused — "+
		"a test process reached a real repository. Inspect it (git config --local --list, git for-each-ref, git worktree list) before trusting it:\n%s",
		runner, root, strings.Join(changes, "\n"))
}
