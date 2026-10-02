package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// Origin is, for the tip of main, how that tip stands to origin/main as
	// fetched when it was read (originBehind, originAhead, originApart), and ""
	// for every other part and when there is no origin/main.
	Origin string
	// Root is, for the checked-out commit, the checkout it was read in, so a
	// move of it can be judged against that checkout's history; "" otherwise.
	Root string
}

// gitWorld is everything the canary watches, in a fixed order.
type gitWorld []gitWorldPart

// snapshotGitWorld reads what a test process reaching the real git would
// change, leaving out what ordinary work on a busy box changes all the time: the
// shared config without its [branch "…"] stanzas (a push writes those), the
// checked-out branch of every worktree that existed at both ends, the
// registrations that are neither a lane nor a gate path (a lane is made and
// pruned, and the gate registers checkouts of its own, all through the run),
// the checked-out commit of the checkout at root, the tip of main with the
// reflog subjects behind it, the set of branch names other than lane/* (not
// their tips: a sibling lane's commit moves its own branch), and the
// operator's global git config. Outside a repository only the global config is
// read.
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
		laneDir := worktreeLaneDir(lane)
		registrations = keepWorktrees(registrations, func(path string) bool { return isWatchedWorktree(path, laneDir) })
		heads = keepWorktrees(heads, func(path string) bool { return !isGateWorktree(path) })
		w = append(w, gitWorldPart{Label: "the worktree registrations", Present: true, Text: registrations})
		w = append(w, gitWorldPart{Label: worktreeHeadsLabel, Present: true, Text: heads})
		w = append(w, gitWorldPart{Label: "the branches", Present: true, Text: withoutLaneBranches(gitOut(lane, "for-each-ref", "--format=%(refname)", "refs/heads"))})
		w = append(w, gitWorldPart{Label: checkedOutLabel, Present: true, Text: strings.TrimSpace(gitOut(lane, "rev-parse", "HEAD")), Root: lane})
		tip := strings.TrimSpace(gitOut(lane, "rev-parse", "--verify", "-q", "refs/heads/main"))
		log := strings.TrimSuffix(gitOut(lane, "log", "-g", "-n", strconv.Itoa(reflogWindow), "--format=%gs", "refs/heads/main"), "\n")
		w = append(w, gitWorldPart{Label: tipOfMainLabel, Present: true, Text: tip + "\n" + log, Origin: mainOriginRelation(lane, tip)})
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

// worktreeHeadsLabel names the part holding what each worktree has checked out,
// and tipOfMainLabel the one holding main's tip and the subjects of its reflog.
const (
	checkedOutLabel    = "the checked-out commit"
	worktreeHeadsLabel = "the worktree HEADs"
	tipOfMainLabel     = "the tip of main"
)

// withoutLaneBranches is a list of branch refs, one per line, without the
// lane/* ones: a lane is made and pruned with its branch through any run.
func withoutLaneBranches(refs string) string {
	var kept []string
	for ref := range strings.SplitSeq(refs, "\n") {
		if !strings.HasPrefix(ref, "refs/heads/lane/") {
			kept = append(kept, ref)
		}
	}
	return strings.Join(kept, "\n")
}

// reflogWindow is how many of main's newest reflog entries the canary reads.
const reflogWindow = 100

// How the tip of main stands to origin/main as fetched.
const (
	// originBehind: the tip is origin/main or an ancestor of it.
	originBehind = "behind"
	// originAhead: origin/main is an ancestor of the tip, which holds more.
	originAhead = "ahead"
	// originApart: neither holds the other, or they share no history.
	originApart = "apart"
)

// mainOriginRelation is how tip stands to origin/main in lane's repository,
// "" when there is no origin/main to compare with.
func mainOriginRelation(lane, tip string) string {
	origin := strings.TrimSpace(gitOut(lane, "rev-parse", "--verify", "-q", "refs/remotes/origin/main"))
	if origin == "" || tip == "" {
		return ""
	}
	switch base := strings.TrimSpace(gitOut(lane, "merge-base", tip, origin)); base {
	case tip:
		return originBehind
	case origin:
		return originAhead
	default:
		return originApart
	}
}

// gainedReflog is the entries main's reflog gained between two readings of its
// newest reflogWindow entries: what sits on top of the part of the earlier
// reading the later one still ends with. A full window loses its oldest
// entries as new ones come in, so the later reading is matched against the
// front of the earlier one. known is false when the two share no entry to
// match on.
func gainedReflog(before, after string) (added []string, known bool) {
	entries := func(text string) []string {
		_, log, _ := strings.Cut(text, "\n")
		if log == "" {
			return nil
		}
		return strings.Split(log, "\n")
	}
	was, now := entries(before), entries(after)
	if len(was) == 0 {
		return now, true
	}
	for k := range now {
		rest := now[k:]
		if len(rest) <= len(was) && slices.Equal(rest, was[:len(rest)]) {
			return now[:k], true
		}
	}
	return nil, false
}

// isMergeEntry reports whether a reflog subject is a merge or a pull (`git
// merge` and `git pull` write "merge <ref>: …" and "pull: …").
func isMergeEntry(subject string) bool {
	return strings.HasPrefix(subject, "merge ") || strings.HasPrefix(subject, "pull")
}

// anyNonMerge reports whether any of the reflog subjects is no merge.
func anyNonMerge(subjects []string) bool {
	return slices.ContainsFunc(subjects, func(subject string) bool { return !isMergeEntry(subject) })
}

// onlyMergesAdvancedMain reports whether main's part after differs from before
// only by merges landing on it: the reflog subjects it gained on top are all
// merges or pulls, there is at least one, and the log below them is the one
// before had. A commit, a reset or a move with no log entry is not one.
func onlyMergesAdvancedMain(before, after string) bool {
	added, known := gainedReflog(before, after)
	return known && len(added) > 0 && !anyNonMerge(added)
}

// mainMoveCounts reports whether main's part moving from was to now is a
// change a leak could have made. A reflog entry that is no merge is one,
// wherever the tip is. Otherwise, with an origin/main to compare with, the tip
// moving counts only when it neither is on origin/main nor holds it: a
// fast-forward to origin/main, and a merge of it, are another lane's normal
// work, and a fixture commit never is. With no origin/main, only merges
// landing on main are ordinary.
func mainMoveCounts(was, now gitWorldPart) bool {
	if was.Text == now.Text {
		return false
	}
	added, known := gainedReflog(was.Text, now.Text)
	if anyNonMerge(added) {
		return true
	}
	if now.Origin == "" {
		return !onlyMergesAdvancedMain(was.Text, now.Text)
	}
	wasTip, _, _ := strings.Cut(was.Text, "\n")
	nowTip, _, _ := strings.Cut(now.Text, "\n")
	if wasTip == nowTip {
		return !known
	}
	return now.Origin == originApart
}

// ownerCommitsOnTop reports whether the checkout at lane moving from the
// commit was to the commit now is the checkout's owner working in it: was is
// an ancestor of now, and every commit between them, merges included, was
// authored and committed under the user.email the repository resolves. A
// runner's window can last minutes, and the lane it watches is the one its
// owner commits in; a leaked fixture commits under an identity of its own, a
// reset or a switch moves to something that is no descendant, and neither is
// this.
func ownerCommitsOnTop(lane, was, now string) bool {
	if was == "" || now == "" || strings.TrimSpace(gitOut(lane, "merge-base", was, now)) != was {
		return false
	}
	owner := strings.TrimSpace(gitOut(lane, "config", "user.email"))
	if owner == "" {
		return false
	}
	// was is an ancestor of now and differs from it, so the range holds at
	// least one commit.
	identities := strings.Fields(gitOut(lane, "log", "--format=%ae %ce", was+".."+now))
	return !slices.ContainsFunc(identities, func(email string) bool { return email != owner })
}

// worktreeLaneDir is the directory the lanes of lane's repository live in:
// <parent of the primary>/.worktrees/<repo>. The gate's shared go scratch dir
// and the mutation area sit in it too, one level down, and a run's test
// processes make their temp dirs there.
func worktreeLaneDir(lane string) string {
	primary := primaryCheckoutRoot(lane)
	if primary == "" {
		primary = lane
	}
	return filepath.Join(filepath.Dir(primary), ".worktrees", filepath.Base(primary))
}

// isGateWorktree reports whether path is a checkout the gate itself makes and
// removes while it works: a trunk preview, a PR merge checkout, a fail-first
// checkout.
func isGateWorktree(path string) bool {
	name := filepath.Base(path)
	for _, prefix := range []string{"gate-trunkpreview-", "gate-prmerge-", "gate-failfirst-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return strings.Contains(filepath.ToSlash(path), "/failfirst-wt/")
}

// isWatchedWorktree reports whether a registration at path is one a leak
// could have made: not a gate checkout, and not a lane of this repository (a
// direct child of laneDir). What a test process of the run registers lies
// deeper, in the run's temp dirs under laneDir, or in the system's; both are
// watched.
func isWatchedWorktree(path, laneDir string) bool {
	return !isGateWorktree(path) && filepath.Dir(path) != laneDir
}

// keepWorktrees is the lines of text, each starting with a worktree's path
// (alone, or before " -> "), whose path keep accepts.
func keepWorktrees(text string, keep func(path string) bool) string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if path, _, _ := strings.Cut(line, " -> "); keep(path) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// sharedWorktreeHeads is what was and now say about the worktrees they both
// have: a lane made or removed between the two is not a worktree whose HEAD
// moved.
func sharedWorktreeHeads(was, now string) (string, string) {
	in := func(text string) map[string]bool {
		paths := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			path, _, _ := strings.Cut(line, " -> ")
			paths[path] = true
		}
		return paths
	}
	wasPaths, nowPaths := in(was), in(now)
	return keepWorktrees(was, func(path string) bool { return nowPaths[path] }),
		keepWorktrees(now, func(path string) bool { return wasPaths[path] })
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
		if was.Label == worktreeHeadsLabel {
			was.Text, now.Text = sharedWorktreeHeads(was.Text, now.Text)
		}
		if was.Label == tipOfMainLabel && !mainMoveCounts(was, now) {
			continue
		}
		if was.Label == checkedOutLabel && now.Root != "" && ownerCommitsOnTop(now.Root, was.Text, now.Text) {
			continue
		}
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
