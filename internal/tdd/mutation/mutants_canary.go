package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
// change, leaving out what ordinary work on a busy box changes all the time: the
// shared config without its [branch "…"] stanzas (a push writes those), every
// worktree's HEAD and the list of worktrees, the checked-out commit of the
// checkout at root, the tip of main, the set of branch names (not their tips: a
// sibling lane's commit moves its own branch), and the operator's global git
// config. Outside a repository only the global config is read.
func snapshotGitWorld(root string) gitWorld {
	var w gitWorld
	if lane := RepoRoot(root); lane != "" {
		common := strings.TrimSpace(gitOut(lane, "rev-parse", "--path-format=absolute", "--git-common-dir"))
		if common != "" {
			config := filePart("the repository's config", filepath.Join(common, "config"))
			config.Text = withoutBranchStanzas(config.Text)
			w = append(w, config)
		}
		registrations, heads := worktreeFacts(gitOut(lane, "worktree", "list", "--porcelain"))
		w = append(w, gitWorldPart{"the worktree registrations", true, registrations})
		w = append(w, gitWorldPart{"the worktree HEADs", true, heads})
		w = append(w, gitWorldPart{"the branches", true, gitOut(lane, "for-each-ref", "--format=%(refname)", "refs/heads")})
		w = append(w, gitWorldPart{"the checked-out commit", true, strings.TrimSpace(gitOut(lane, "rev-parse", "HEAD"))})
		w = append(w, gitWorldPart{"the tip of main", true, strings.TrimSpace(gitOut(lane, "rev-parse", "--verify", "-q", "refs/heads/main"))})
	}
	for _, path := range globalGitConfigs() {
		w = append(w, filePart("the global git config "+path, path))
	}
	return w
}

// withoutBranchStanzas is a git config without its [branch "…"] sections, which
// record upstreams and which every push writes.
func withoutBranchStanzas(config string) string {
	if config == "" {
		return ""
	}
	var kept []string
	inBranch := false
	for _, line := range strings.Split(strings.TrimSuffix(config, "\n"), "\n") {
		if header := strings.TrimSpace(line); strings.HasPrefix(header, "[") {
			inBranch = strings.HasPrefix(header, `[branch "`)
		}
		if !inBranch {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n") + "\n"
}

// worktreeFacts reads `git worktree list --porcelain` into the paths of the
// registered worktrees and what each has checked out (a branch, or detached),
// one per line, sorted by the listing's own order.
func worktreeFacts(porcelain string) (registrations, heads string) {
	var paths, checkouts []string
	path := ""
	for _, line := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
			paths = append(paths, path)
		case strings.HasPrefix(line, "branch "):
			checkouts = append(checkouts, path+" -> "+strings.TrimPrefix(line, "branch "))
		case line == "detached":
			checkouts = append(checkouts, path+" -> detached")
		}
	}
	slices.Sort(paths)
	slices.Sort(checkouts)
	return strings.Join(paths, "\n"), strings.Join(checkouts, "\n")
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
	if len(w) != len(after) {
		// One side is a repository and the other is not: what is watched is
		// itself different, which no test process should be able to do.
		return []string{fmt.Sprintf("what is watched changed (%d things, then %d): a repository appeared or went away", len(w), len(after))}
	}
	var changes []string
	for i, was := range w {
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
// recorded as an escape with every detail; the caller refuses its result with
// gitWorldRefusal, one line. A quiet run prints nothing.
func (g gitWorldWatch) verify(log io.Writer) []string {
	changes := g.before.changesTo(snapshotGitWorld(g.root))
	if len(changes) == 0 {
		return nil
	}
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

// gitWorldRefusal is the one line a refused result carries: what changed and
// where. The lines gained and lost go to the escape record.
func gitWorldRefusal(runner, root string, changes []string) string {
	labels := make([]string, len(changes))
	for i, change := range changes {
		first, _, _ := strings.Cut(change, "\n")
		labels[i], _, _ = strings.Cut(first, " changed")
	}
	return fmt.Sprintf("gate: refused — a test process of the %s changed %s in %s or the global git config; its result is not trusted (details in the escape record)",
		runner, strings.Join(labels, ", "), root)
}
