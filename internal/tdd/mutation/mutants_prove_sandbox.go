package mutation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// A mutated program can do anything its code can, git and filesystem writes
// included, and a mutant that skips a guard runs that code wherever the test
// process stands. A proof that mutated the lane and ran the tests there lost
// a lane's uncommitted work to exactly that: a skipped error check let an
// empty checkout path reach `git read-tree -u --reset`, which reset the
// lane's whole working tree (#972). So a proof never runs in the lane. It
// runs in a disposable copy holding the lane's current state, and the lane
// is only ever read.
//
// The copy is its own repository, not a worktree of the lane's: a worktree
// shares the lane's refs, index directory and config, so a mutant's git
// write would still land in the lane's repository. Its objects are borrowed
// read-only through an alternates file, so nothing is copied from the object
// store; its refs, HEAD, index and config are copies, and it has no remote a
// mutant could push into. The working files are copied, never hardlinked: a
// write through a hardlink is a write to the lane's own file.

// proveSandbox is one proof's copy of a lane.
type proveSandbox struct {
	// area holds every proof's copy of this lane; dir is this proof's own
	// directory inside it, the one cleanup removes; root is the copy of the
	// lane itself, inside dir.
	area, dir, root string
	links           depinstall.Links
	once            sync.Once
}

// proveSandboxArea is where proofs of lane make their copies: beside the
// lane in the `.mutants` area the measurement uses, on the lane's own disk
// and never inside it, since a copy inside the lane is a directory the copy
// would copy into itself.
func proveSandboxArea(lane string) string {
	return filepath.Join(filepath.Dir(lane), ".mutants", "prove-"+filepath.Base(lane))
}

// newProveSandbox copies lane, the repository root, with projectRoot's
// installed node_modules linked in, and answers the copy. A failure removes
// whatever part of the copy was made.
func newProveSandbox(lane, projectRoot string) (*proveSandbox, error) {
	return newWorkerSandbox(lane, projectRoot, 0)
}

// newWorkerSandbox is newProveSandbox for the nth concurrent worker of a
// run, which keeps its copy in the slot workerSlot(n) names.
func newWorkerSandbox(lane, projectRoot string, worker int) (*proveSandbox, error) {
	area := proveSandboxArea(lane)
	if err := os.MkdirAll(area, 0o755); err != nil {
		return nil, err
	}
	dir, err := claimSandboxSlot(area, workerSlot(worker))
	if err != nil {
		return nil, err
	}
	sb := &proveSandbox{area: area, dir: dir, root: filepath.Join(dir, filepath.Base(lane))}
	if err := sb.build(lane, projectRoot); err != nil {
		sb.remove()
		return nil, err
	}
	return sb, nil
}

// proveSandboxSlot is the one directory in the area proofs of a lane reuse.
// The go build cache keys a compile on the directory it ran in, so a copy at
// a new path every time rebuilds everything the tested package imports —
// measured at 1.8 s of a 2.5 s proof, against 0.3 s for the copy itself.
const proveSandboxSlot = "tree"

// proveSandboxRunPrefix starts the name of a proof's directory when the slot
// is held by another.
const proveSandboxRunPrefix = "run-"

// proveSandboxHolder is the record a proof leaves in the directory it holds:
// its pid and its process identity (boot id and start time), so the next
// proof, and gc, can tell a killed proof's leftover from a copy a proof is
// running in now, even when the pid has since been reused.
const proveSandboxHolder = ".aphrollo-prove-holder"

// workerSlot is the name of the slot the nth concurrent worker of one run
// keeps its copy in: the first keeps the proofs' slot, every other one a slot
// of its own, so each worker's copy sits at the same path in every run and
// the go build cache, which keys a compile on the directory, stays warm for
// all of them. A run-w slot is a proof's directory to gc like any run- one.
func workerSlot(n int) string {
	if n <= 0 {
		return proveSandboxSlot
	}
	return proveSandboxRunPrefix + "w" + strconv.Itoa(n)
}

// claimSandboxSlot takes the reusable slot called name in area when it is
// free, or when the proof holding it is gone, and a directory of its own
// otherwise. A slot whose holder cannot be read is treated as held: it may be
// a proof that has made it and not yet written its record.
func claimSandboxSlot(area, name string) (string, error) {
	slot := filepath.Join(area, name)
	if err := os.Mkdir(slot, 0o755); err == nil {
		return holdSandboxDir(slot)
	}
	if !sandboxHeld(slot) && depinstall.RemoveTree(slot) == nil && os.Mkdir(slot, 0o755) == nil {
		return holdSandboxDir(slot)
	}
	dir, err := os.MkdirTemp(area, proveSandboxRunPrefix)
	if err != nil {
		return "", err
	}
	return holdSandboxDir(dir)
}

// holdSandboxDir records this process as dir's holder and answers dir. A
// directory whose record cannot be written is removed rather than left
// looking like a leftover.
func holdSandboxDir(dir string) (string, error) {
	identity, _ := processIdentityFn(os.Getpid())
	record := fmt.Sprintf("pid=%d\nidentity=%s\n", os.Getpid(), identity)
	if err := os.WriteFile(filepath.Join(dir, proveSandboxHolder), []byte(record), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// sandboxHeld reports whether a live process holds dir. A record that
// cannot be read or parsed counts as held; one that names an identity is held
// only while the process running under that pid still has it, since a pid is
// reused after a reboot. A record with no identity is judged by its pid.
func sandboxHeld(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, proveSandboxHolder))
	if err != nil {
		return true
	}
	var pid int
	var identity string
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "pid":
			pid, err = strconv.Atoi(value)
			if err != nil {
				return true
			}
		case "identity":
			identity = value
		}
	}
	if pid == 0 {
		return true
	}
	if !pidRunningFn(pid) {
		return false
	}
	if identity == "" {
		return true
	}
	now, ok := processIdentityFn(pid)
	return !ok || now == identity
}

// proveAreaHeld reports whether a live proof holds any of the directories a
// proof makes in area, the `.mutants/prove-<lane>` gc must not delete under
// it. Any other directory in area is not a proof's and holds nothing.
func proveAreaHeld(area string) bool {
	entries, err := os.ReadDir(area)
	if err != nil {
		return false
	}
	for _, e := range entries {
		proofs := e.Name() == proveSandboxSlot || strings.HasPrefix(e.Name(), proveSandboxRunPrefix)
		if e.IsDir() && proofs && sandboxHeld(filepath.Join(area, e.Name())) {
			return true
		}
	}
	return false
}

// path maps p, a path inside lane, to the same place in the copy. Both
// sides are compared canonically (repoRelSlashPath), so two spellings of one
// directory still agree; a p that is not inside lane has no place in the
// copy and is an error, never a path outside it.
func (sb *proveSandbox) path(lane, p string) (string, error) {
	rel, ok := repoRelSlashPath(lane, p)
	if !ok || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("%s is not inside %s", p, lane)
	}
	return filepath.Join(sb.root, filepath.FromSlash(rel)), nil
}

// remove deletes the copy, unlinking every link first so the deletion never
// reaches the lane's node_modules through one, then the area when no other
// proof is using it. Safe to call more than once, and from the signal
// handler while the proof's own goroutine is still running.
func (sb *proveSandbox) remove() {
	sb.once.Do(func() {
		sb.links.Remove()
		_ = depinstall.RemoveTree(sb.dir)
		_ = os.Remove(sb.area)
	})
}

// build fills the copy: the working files first, then the repository that
// describes them, then the dependency links.
func (sb *proveSandbox) build(lane, projectRoot string) error {
	if err := copyWorkingFiles(lane, sb.root); err != nil {
		return err
	}
	if err := copyRepository(lane, sb.root); err != nil {
		return err
	}
	return sb.linkNodeModules(lane, projectRoot)
}

// copyWorkingFiles copies every file of lane's working tree git would show
// — tracked files as they are on disk now, and untracked ones that are not
// ignored — into root. A tracked file deleted from disk is not copied, so the
// copy shows the same deletion.
func copyWorkingFiles(lane, root string) error {
	listed, err := git(lane, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return fmt.Errorf("git ls-files: %v", err)
	}
	seen := map[string]bool{}
	for _, rel := range strings.Split(listed, "\x00") {
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		src := filepath.Join(lane, filepath.FromSlash(rel))
		if err := copyEntry(src, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return os.MkdirAll(root, 0o755)
}

// copyEntry copies one listed path: a regular file by content, a symlink as
// a symlink. Anything else — a gitlink's directory, a path gone from disk —
// is not a file of this tree to copy.
func copyEntry(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		// absence-ok: a tracked path deleted from disk is part of the lane's
		// state, and leaving it out of the copy is how the copy carries it.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.Mode().IsRegular():
		return copyFileContent(src, dst, fi.Mode().Perm())
	}
	return nil
}

// copyFileContent writes a new file at dst holding src's bytes. io.Copy
// between two files uses the kernel's own copy where it has one, which a
// copy-on-write filesystem answers with a reflink.
func copyFileContent(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// copyRepository makes root a repository of its own describing the same
// state as lane's: lane's objects borrowed through alternates, its refs,
// HEAD, index and config copied, and every remote dropped.
func copyRepository(lane, root string) error {
	paths, err := git(lane, "rev-parse", "--path-format=absolute",
		"--git-path", "objects", "--git-path", "index", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("git rev-parse: %v", err)
	}
	p := strings.Split(strings.TrimSpace(paths), "\n")
	if len(p) != 3 {
		return fmt.Errorf("git rev-parse answered %q", paths)
	}
	objects, index, common := p[0], p[1], p[2]
	if _, err := git(root, "init", "-q", "--template="); err != nil {
		return fmt.Errorf("git init: %v", err)
	}
	gitDir := filepath.Join(root, ".git")
	if err := copyFileOver(filepath.Join(common, "config"), filepath.Join(gitDir, "config")); err != nil {
		return err
	}
	// The copy sits deeper than the lane, and a git call inside it on a long
	// path fails on Windows without this.
	for _, args := range [][]string{{"config", "core.bare", "false"}, {"config", "core.longpaths", "true"}, {"config", "--unset-all", "core.worktree"}} {
		_, _ = git(root, args...)
	}
	for _, remote := range strings.Fields(gitOut(root, "remote")) {
		if _, err := git(root, "config", "--remove-section", "remote."+remote); err != nil {
			return fmt.Errorf("the copy kept remote %q: %v", remote, err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "objects", "info", "alternates"), []byte(objects+"\n"), 0o644); err != nil {
		return err
	}
	if err := copyRefs(lane, root); err != nil {
		return err
	}
	if err := copyHead(lane, root); err != nil {
		return err
	}
	// A lane with nothing ever staged has no index file to copy.
	var indexErr error
	if _, err := os.Stat(index); err == nil {
		indexErr = copyFileOver(index, filepath.Join(gitDir, "index"))
	}
	// The copied index carries the lane's stat data, which matches no file
	// in the copy; refreshing it now keeps the copy's own git status and
	// diffs from re-hashing every file. Its exit code only says whether any
	// file differs from the index, which it may.
	_, _ = git(root, "update-index", "-q", "--refresh")
	return indexErr
}

// copyFileOver replaces dst with a copy of src.
func copyFileOver(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	_ = os.Remove(dst)
	return copyFileContent(src, dst, fi.Mode().Perm())
}

// copyRefs gives root every ref lane can see, each naming the same object.
func copyRefs(lane, root string) error {
	refs, err := git(lane, "for-each-ref", "--format=create %(refname) %(objectname)")
	if err != nil {
		return fmt.Errorf("git for-each-ref: %v", err)
	}
	if strings.TrimSpace(refs) == "" {
		return nil
	}
	if out, err := gitStdin(root, strings.NewReader(refs), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("git update-ref: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// copyHead points root's HEAD where lane's points: at the same branch, or at
// the same commit when lane's HEAD is detached.
func copyHead(lane, root string) error {
	if branch, err := git(lane, "symbolic-ref", "-q", "HEAD"); err == nil {
		_, err := git(root, "symbolic-ref", "HEAD", strings.TrimSpace(branch))
		return err
	}
	sha, err := git(lane, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("the lane's HEAD resolves to nothing: %v", err)
	}
	_, err = git(root, "update-ref", "--no-deref", "HEAD", strings.TrimSpace(sha))
	return err
}

// linkNodeModules links each node_modules the lane has installed, from the
// project root up to the lane's root, into the same place in the copy.
// Node resolves a package by walking up from the importing file, so a
// monorepo's root install counts as much as the project's own. An install
// is not work in progress; a link is recorded so removal unlinks it rather
// than deleting through it.
func (sb *proveSandbox) linkNodeModules(lane, projectRoot string) error {
	for dir := projectRoot; ; dir = filepath.Dir(dir) {
		installed := filepath.Join(dir, depinstall.NodeModules)
		if fi, err := os.Stat(installed); err == nil && fi.IsDir() {
			link, err := sb.path(lane, installed)
			if err != nil {
				return err
			}
			if err := sb.links.Make(installed, link); err != nil {
				return fmt.Errorf("linking %s: %v", installed, err)
			}
		}
		if rel, err := filepath.Rel(lane, dir); err != nil || rel == "." || !filepath.IsLocal(rel) {
			return nil
		}
	}
}

// proveTargetDir is the one cargo target directory every proof of lane
// builds into: in the lane's measurement area, beside the measurement's own
// per-shard target dirs and swept by the same gc rule, so the second proof
// builds warm. The copy's own target would be cold every time and deleted
// with it; the lane's is inside the lane.
func proveTargetDir(lane string) string {
	return filepath.Join(measureTempDir(lane), "target-prove")
}

// buildOutsideTheCopy points every cargo command run runs at lane's proof
// target directory. A CARGO_TARGET_DIR the caller set already names where
// the bytes land, and is left alone.
func buildOutsideTheCopy(run SuiteRunner, lane string) SuiteRunner {
	if strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR")) != "" {
		return run
	}
	target := proveTargetDir(lane)
	return func(r Runner, dir string) SuiteResult {
		if r.Cmd == "cargo" {
			r.Env = append(slices.Clone(r.Env), "CARGO_TARGET_DIR="+target)
		}
		return run(r, dir)
	}
}
