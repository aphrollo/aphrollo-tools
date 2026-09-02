package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The unattended half of the sweep: what runs without anyone typing
// `aphrollo gate gc`. Two triggers, deliberately different in character:
//
//   - a worktree removal deletes IMMEDIATELY and synchronously — the
//     operator just said that tree is finished, and its build dir is the
//     single biggest thing on the disk;
//   - a session start delegates to a DETACHED sweep at most once a day, and
//     the session AFTER it reports what was freed. A hook that walked
//     hundreds of gigabytes inline would stall every session start, and one
//     that waited for its own child would be the same stall with extra
//     steps.

// gcStampFile / gcReportFile live in the state dir: when the last sweep ran,
// and what it freed (pending a session to report it).
const (
	gcStampFile    = "gc-last-run"
	gcReportFile   = "gc-last-report.json"
	gcSweepEvery   = 24 * time.Hour
	gcOwnerCommand = "aphrollo gate gc --apply"
)

// gcReport is one completed sweep, awaiting a session to surface it.
type gcReport struct {
	Freed    int64     `json:"freed"`
	Dirs     int       `json:"dirs"`
	At       time.Time `json:"at"`
	Reported bool      `json:"reported"`
}

// gcDue reports whether a background sweep should start now: never more
// than once per gcSweepEvery, so a morning of five sessions walks the disk
// once. An unreadable/absent stamp means due — the failure mode is one
// extra scan, not a missed one.
func gcDue(now time.Time) bool {
	path := gcStatePath(gcStampFile)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return now.Sub(info.ModTime()) >= gcSweepEvery
}

// claimGCSweep is gcDue and stampGC as ONE step: the claim IS the stamp, so
// two sessions starting together cannot both decide a sweep is due and both
// launch a RemoveAll walk over the same tree. The claim is an O_EXCL create
// of a fresh marker, renamed onto the stamp — the filesystem picks the
// winner.
func claimGCSweep(now time.Time) bool {
	path := gcStatePath(gcStampFile)
	if path == "" {
		return false
	}
	if !gcDue(now) {
		return false
	}
	claim := path + ".claim"
	f, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// Someone else is claiming right now, or a previous claim was left
		// behind: an abandoned claim older than the sweep interval is stale
		// and is cleared so the next session start can win it.
		if info, serr := os.Stat(claim); serr == nil && now.Sub(info.ModTime()) >= gcSweepEvery {
			_ = os.Remove(claim)
		}
		return false
	}
	_, _ = f.WriteString(now.UTC().Format(time.RFC3339))
	_ = f.Close()
	if err := os.Rename(claim, path); err != nil {
		_ = os.Remove(claim)
		return false
	}
	_ = os.Chtimes(path, now, now)
	return true
}

// stampGC records that a sweep started. It is stamped at START, not at
// completion: a sweep that dies halfway must not re-run on every session
// start until it finally finishes.
func stampGC(now time.Time) {
	path := gcStatePath(gcStampFile)
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)), 0o600)
	_ = os.Chtimes(path, now, now)
}

// writeGCReport records what a completed sweep freed, for the next session
// to surface. Best-effort: losing the report costs a line of output.
func writeGCReport(freed int64, dirs int) {
	path := gcStatePath(gcReportFile)
	if path == "" {
		return
	}
	data, err := json.Marshal(gcReport{Freed: freed, Dirs: dirs, At: time.Now().UTC()})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// RecordGCSweep records a completed sweep for the next session start to
// surface. Exported for internal/cli, which owns the command that performs
// the sweep — including the detached one that has nowhere to print.
func RecordGCSweep(freed int64, dirs int) { writeGCReport(freed, dirs) }

// gcReportLine returns the ONE line a session start surfaces about the last
// sweep, and marks it reported so the next session stays silent. "" when
// there is no report, it was already reported, or it freed nothing.
func gcReportLine() string {
	path := gcStatePath(gcReportFile)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var r gcReport
	if err := json.Unmarshal(data, &r); err != nil || r.Reported || r.Freed <= 0 {
		return ""
	}
	r.Reported = true
	if marked, err := json.Marshal(r); err == nil {
		_ = os.WriteFile(path, marked, 0o600)
	}
	return fmt.Sprintf("gate gc: reclaimed %s across %d stale build directories (idle incremental caches, dead gate dirs, orphan worktree builds, stray target dirs). `aphrollo gate gc` lists what is left.",
		formatBytes(r.Freed), r.Dirs)
}

// gcStatePath resolves a state-dir file, creating the dir. "" when there is
// no state dir at all (then nothing about the sweep is remembered, which is
// the same as it never having run).
func gcStatePath(name string) string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return filepath.Join(dir, name)
}

// gcSpawnForTest replaces the detached sweep with an observer. Always nil in
// production.
var gcSpawnForTest func(cwd string)

// maybeStartBackgroundGC starts a detached `aphrollo gate gc --apply` for cwd
// when one is due, and stamps immediately so concurrent session starts do
// not each launch one. It never waits: the child outlives this process, and
// the session AFTER it reports the result.
func maybeStartBackgroundGC(cwd string) {
	if cwd == "" || !claimGCSweep(time.Now()) {
		return
	}
	if gcSpawnForTest != nil {
		gcSpawnForTest(cwd)
		return
	}
	spawnBackgroundGC(cwd)
}

// spawnBackgroundGC launches this same binary as a detached sweep. Failure
// is silent by design: disk hygiene must never disturb a session start.
func spawnBackgroundGC(cwd string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, CmdName, "gc", "--apply", "--quiet", "--repo", cwd)
	cmd.Dir = cwd
	cmd.Env = cleanGitEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// Detached: the stamp fires before the work, so a sweep killed with the
	// hook's process group would leave a half-deleted tree and no sweep due
	// for another day.
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}

// backgroundGCSpawnDescription states the spawn's contract for a test that
// cannot watch a process die with its parent.
func backgroundGCSpawnDescription() string {
	if detachedAttrs() == nil {
		return "attached to the hook"
	}
	return "detached from the hook's process group"
}

// writeGateOrigin records which repo a hash-named gate directory belongs to.
// Written at creation and never read by the gate itself: it exists so the
// sweep can tell a live directory from the remains of a deleted repo, which
// the hash alone can never say. Best-effort — a missing origin only makes
// the directory UNKNOWN, and unknown directories are left alone.
func writeGateOrigin(dir, root string) {
	if dir == "" || root == "" {
		return
	}
	path := filepath.Join(dir, gcOriginFile)
	if data, err := os.ReadFile(path); err == nil && string(data) == root {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(root), 0o600)
}

// GCAfterWorktreeChange sweeps what a `git worktree remove`/`prune` left
// behind for repoRoot, right now: the removed tree's own build dir (git
// deletes the checkout, never the target/ inside it), any other orphan build
// dir beside the registered worktrees, and the gate's fail-first worktree /
// warm target for roots that no longer exist. Returns the bytes freed.
// Incremental caches are NOT in scope here — nothing about removing a
// worktree says the shared target dir is idle.
func GCAfterWorktreeChange(repoRoot, removed string) int64 {
	var freed int64
	// The leftover build dir goes FIRST, and on its own: while it exists the
	// removed worktree path still exists, so the gate dirs keyed on that
	// path would read as live and survive a combined sweep.
	if removed != "" {
		if _, err := os.Stat(removed); err == nil && onlyBuildDirInside(removed) && !gcProtected(removed) {
			_, size := dirNewestAndSize(removed)
			f, _, _ := ApplyGCFor(repoRoot, []GCCandidate{{
				Path:   removed,
				Size:   size,
				Reason: "build dir left behind by git worktree remove",
				Kind:   GCKindOrphanWorktree,
			}})
			freed += f
		}
	}
	rest := ScanGC(repoRoot, DefaultGCAge, GCScope{GateDirs: true, OrphanWorktrees: true})
	f, _, _ := ApplyGCFor(repoRoot, dedupeCandidates(rest))
	return freed + f
}

// dedupeCandidates drops a path proposed twice (the removed worktree is also
// an orphan build dir), so a sweep never counts one directory's bytes twice.
func dedupeCandidates(cands []GCCandidate) []GCCandidate {
	seen := map[string]bool{}
	out := cands[:0]
	for _, c := range cands {
		key := filepath.Clean(c.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
