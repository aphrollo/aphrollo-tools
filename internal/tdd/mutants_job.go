package tdd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A mutation run is started by the COMMIT, not by the merge. Waiting until a
// session asks to merge is what made the proof a multi-hour wall in front of
// the one action that needed it; started at commit time, the run has the whole
// review to finish in and the merge is usually a lookup.
//
// Three rules keep that from costing more than it saves:
//
//	one worktree — the job runs in <parent>/.worktrees/<repo>/mutants, checked
//	   out to the tip, with its own persistent target dir. cargo-mutants'
//	   default tree COPY put 135 MB per run in the OS temp dir and rebuilt the
//	   world cold every time (11 copies, ~1.5 GB, measured on one box).
//	never cancelled — a job for an older tip of the same lane finishes. Its
//	   outcomes are what the next tip's run carries instead of re-measuring,
//	   so cancelling it throws away exactly the work that makes the next run
//	   cheap.
//	out of the queue — it owns its target dir, so it takes no build slot and
//	   never serializes against an editor's post-edit hook (which waited up to
//	   619 s behind a bare mutants run, 56 hooks deferred in 3 h).

// MutantsJob is one detached mutation run, recorded so a merge can name it
// and a statusline can show it.
type MutantsJob struct {
	Schema int `json:"schema"`
	// Repo is the git COMMON dir — the one directory every worktree of a repo
	// shares, and therefore what "the same repo" means here.
	Repo string `json:"repo"`
	// RepoID is the same repository named independently of where it is
	// checked out; see MutationReceipt.RepoID.
	RepoID    string `json:"repo_id,omitempty"`
	RepoRoot  string `json:"repo_root"`
	Branch    string `json:"branch"`
	Tip       string `json:"tip"`
	TipTree   string `json:"tip_tree"`
	BaseRef   string `json:"base_ref"`
	BaseSHA   string `json:"base_sha"`
	Worktree  string `json:"worktree"`
	TargetDir string `json:"target_dir"`
	Diff      string `json:"diff"`
	// Log and ErrLog are the job's own stdout and stderr, as FILES: a
	// detached run has nowhere to print, and a run that dies without them
	// takes the reason with it. They live in the mutation worktree's build
	// directory and never in the source tree — a stray untracked file there
	// makes the receipt's own dirty check fail the run it describes.
	Log    string `json:"log"`
	ErrLog string `json:"err_log"`
	PID    int    `json:"pid"`
	// PIDStart is the OS's own record of when THAT process started, recorded
	// beside the pid because a pid is not an identity: this registry outlives
	// a reboot, and a recycled pid answered "still running" for a completely
	// different process. Empty on a record written before this existed, or on
	// a box that cannot report it, in which case the pid alone decides.
	PIDStart string    `json:"pid_start,omitempty"`
	Started  time.Time `json:"started"`
}

// mutantsJobMaxAge is how long a recorded job may claim to be running before
// it is dropped whatever its pid says. A pid is reusable; a mutation run that
// has been going for half a day is not one anybody is waiting for.
const mutantsJobMaxAge = 12 * time.Hour

// StartMutantsJob starts the lane's mutation run, detached. It reports false
// for everything that is not a lane commit in an opted-in repo, which is the
// common case and must cost nothing.
func StartMutantsJob(repoRoot string) (MutantsJob, bool) {
	j, ok, _ := startMutantsJob(repoRoot)
	return j, ok
}

// startMutantsJob is StartMutantsJob's full form. Its error is nil on every
// SILENT skip (not a lane, not opted in, disk refused — each already prints
// or logs its own reason) and non-nil only when a real attempt to start the
// run failed after committing to try, so PostCommitHook — the one caller with
// somewhere to put it — can tell an operator what happened instead of
// printing "started" for a run that never began.
func startMutantsJob(repoRoot string) (MutantsJob, bool, error) {
	root := RepoRoot(repoRoot)
	if root == "" {
		return MutantsJob{}, false, nil
	}
	branch := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || isDefaultBranch(branch) || !mutationReceiptOptIn(root) || !mutationRunsLocally(root) {
		return MutantsJob{}, false, nil
	}
	j := MutantsJob{
		Schema: StateSchema, Repo: commonGitDir(root), RepoID: repoIdentity(root), RepoRoot: root, Branch: branch,
		Tip: gitOut(root, "rev-parse", "HEAD"), TipTree: gitOut(root, "rev-parse", "HEAD:"),
		BaseRef: laneBaseRef(root), Worktree: MutantsWorktreeDir(root), TargetDir: MutantsTargetDir(root),
	}
	j.BaseSHA = gitOut(root, "merge-base", j.BaseRef, "HEAD")
	if j.Tip == "" || j.TipTree == "" || j.BaseSHA == "" {
		return MutantsJob{}, false, nil
	}
	j.Diff = filepath.Join(mutantsStateDir(), "lane."+projectKey(root)+".diff")
	// Beside the gate's other state, never inside the worktree cargo-mutants
	// mutates in place: a log file created there before the worktree exists is
	// a log the worktree's own creation can never reach, and one written
	// during a run is an untracked file that makes the run's own dirty check
	// fail the tree it is describing.
	logDir := mutantsLogDir(j.RepoRoot)
	j.Log = filepath.Join(logDir, short(j.TipTree)+".log")
	j.ErrLog = filepath.Join(logDir, short(j.TipTree)+".err.log")
	j.Started = time.Now()
	// Before anything is spawned: a run that cannot fit its copies fills the
	// drive and dies mid-way, taking every verdict it had reached with it.
	if jobs, _ := mutantsJobsForThisBox(); true {
		if ok, line := mutantsDiskOK(j.TargetDir, jobs); !ok {
			appendGateLog("postcommit", logToken(j.Repo), "mutants", "mutants-refused:disk", 0)
			fmt.Fprintln(os.Stderr, line)
			return MutantsJob{}, false, nil
		}
	}
	// The worktree is prepared HERE, synchronously, rather than left to the
	// detached child: a bad path (or a drive that cannot create it) is then
	// caught before this process ever reports the run as started, instead of
	// the child discovering it seconds later with nowhere left to say so but
	// a log line nobody watching the hook's own output would see.
	if err := prepareMutantsWorktree(j); err != nil {
		appendGateLog("postcommit", logToken(j.Repo), "mutants", "mutants-worktree-failed:"+logToken(err.Error()), 0)
		return MutantsJob{}, false, err
	}
	pid, err := mutantsSpawnFn(j)
	if err != nil {
		appendGateLog("postcommit", logToken(j.Repo), "mutants", "mutants-start-failed", 0)
		return MutantsJob{}, false, err
	}
	j.PID = pid
	j.PIDStart = processStartToken(pid)
	saveMutantsJob(j)
	appendGateLog("postcommit", logToken(j.Repo), "mutants", "mutants-started:"+short(j.TipTree), 0)
	return j, true, nil
}

// mutantsLogDir is where a job's own stdout/stderr live: beside the gate's
// other state, keyed on the repo, never inside the mutation worktree.
func mutantsLogDir(repoRoot string) string {
	dir := mutantsStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, projectKey(repoRoot))
}

// mutantsSpawnFn is the detached start, a seam so a test can prove the
// lifecycle without a mutation run.
var mutantsSpawnFn = spawnMutantsJob

// defaultBranches are the branch names that are not a lane. A commit landing
// on one of them is not being prepared for a merge, so there is nothing for a
// mutation run to prove.
var defaultBranches = map[string]bool{"main": true, "master": true}

func isDefaultBranch(branch string) bool { return defaultBranches[strings.TrimSpace(branch)] }

// laneBaseRef is the ref a lane's diff is taken against: the remote's default
// branch when there is one, the local one otherwise. Its RESOLVED sha is what
// the receipt records — a ref name moves.
func laneBaseRef(root string) string {
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return ref
		}
	}
	return "HEAD~1"
}

// mutationReceiptOptIn reads the repo's opt-in, from whichever manifest it
// has: a Cargo workspace declares it in `[workspace.metadata.aphrollo]`, and
// a repo with no Cargo.toml (Go, Python) in a root aphrollo.toml.
func mutationReceiptOptIn(root string) bool {
	ws := cargoWorkspaceRoot(root)
	if ws == "" {
		ws = root
	}
	return cargoAphrolloFlag(ws, "mutation-receipt") || aphrolloTomlFlag(root, "mutation-receipt")
}

// aphrolloTomlFlag reads one boolean from `[aphrollo]` in <root>/aphrollo.toml.
func aphrolloTomlFlag(root, key string) bool {
	return tomlBoolIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", key)
}

// aphrolloTomlString reads one scalar STRING key from `[aphrollo]`.
func aphrolloTomlString(root, key string) (string, bool) {
	return tomlStringIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", key)
}

// mutationRunsLocally says whether the lane's proof is measured on THIS box.
// A repo with a CI runner that can do it says `mutants-local = false` beside
// its opt-in and the post-commit hook stops starting detached runs here: one
// Go mutant costs a whole re-run of its package, and the runner has a Linux
// process spawn where this box has a Windows one. Absent means yes — a repo
// that has said nothing keeps the behaviour it already has.
func mutationRunsLocally(root string) bool {
	if v, set := tomlBoolSetIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", "mutants-local"); set {
		return v
	}
	// cargoWorkspaceRoot answers `root` when it finds no workspace table, so a
	// single-crate repo's own Cargo.toml is what gets read here.
	ws := cargoWorkspaceRoot(root)
	if v, set := tomlBoolSetIn(filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]", "mutants-local"); set {
		return v
	}
	return true
}

// mutationJudgedLocally reports whether THIS box may judge a lane's mutation
// receipt. It defaults to mutationRunsLocally, because the two normally agree:
// the box that measures is the box that holds the receipt and the key it was
// signed with.
//
// They come apart when the run is measured elsewhere UNDER THE SAME KEY -- a
// Linux clone sharing CLAUDE_CONFIG_DIR, which is how this repo earns receipts
// a Windows gremlins cannot produce. There the receipt is present and
// verifiable, so standing the gate down would waive a proof that exists, and
// every lane would merge on nothing. `mutants-judge-local` says which answer
// applies when they differ; unset, nothing changes for any repo.
func mutationJudgedLocally(root string) bool {
	if v, set := tomlBoolSetIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", "mutants-judge-local"); set {
		return v
	}
	ws := cargoWorkspaceRoot(root)
	if v, set := tomlBoolSetIn(filepath.Join(ws, "Cargo.toml"), "[workspace.metadata.aphrollo]", "mutants-judge-local"); set {
		return v
	}
	return mutationRunsLocally(root)
}

// MutantsWorktreeDir is the ONE dedicated worktree a repo's mutation runs use,
// beside the lane worktrees rather than inside the checkout: a build dir under
// the checkout is one a lane's own tooling would find and sweep.
//
// It is keyed on the PRIMARY checkout, never on repoRoot directly: repoRoot is
// routinely a LANE worktree (a commit fires the post-commit hook from wherever
// it was made), and deriving straight from it nested the mutants tree under
// the lane's own `.worktrees` entry instead of the repo's
// (`<parent-of-primary>/.worktrees/<repo>/mutants`) — a path `git worktree add`
// never has a reason to create, so every run failed at that step. The primary
// checkout is `--git-common-dir`'s PARENT: that path names the one `.git`
// directory every worktree of the repo shares, regardless of which one asked.
func MutantsWorktreeDir(repoRoot string) string {
	return filepath.Join(MutantsRootDir(repoRoot), mutantsLaneKey(repoRoot))
}

// MutantsRootDir is the repo's one mutants directory, holding a subdirectory
// per lane. Every containment check keys on this rather than on a lane's own
// tree: the build-slot bypass, the gc sweep and the primary-checkout guardrail
// all ask "is this path inside the repo's mutants area", a question that must
// stay true however many lanes are measuring.
func MutantsRootDir(repoRoot string) string {
	primary := primaryCheckoutRoot(repoRoot)
	if primary == "" {
		primary = filepath.Clean(repoRoot)
	}
	return filepath.Join(filepath.Dir(primary), ".worktrees", filepath.Base(primary), "mutants")
}

// mutantsLaneKey separates one lane's measurement from another's. A repo-wide
// tree was warm and cheap with one lane in flight and destructive with two: a
// commit in any lane fires the post-commit hook, and the detached run checked
// the shared worktree out to its own tip under whichever run was still working
// in it. Four concurrent lanes in borld produced `Worker thread failed: The
// file exists. (os error 80)`, jobs evicted from the registry by later lanes,
// and no receipt for anyone -- which blocks every merge, since the receipt is
// what the merge gate consumes.
//
// The key is the LANE'S OWN CHECKOUT, not its branch: a branch is renameable
// and carries path separators, while the worktree path is where the run
// actually operates and is stable across the lane's commits, so the tree stays
// warm exactly as long as the lane does.
func mutantsLaneKey(repoRoot string) string {
	lane := RepoRoot(repoRoot)
	if lane == "" {
		lane = filepath.Clean(repoRoot)
	}
	return projectKey(lane)
}

// primaryCheckoutRoot resolves repoRoot's PRIMARY checkout — the directory
// holding the `.git` that `--git-common-dir` names — or "" when repoRoot is
// not (yet) a git repository at all, in which case the caller falls back to
// treating repoRoot as its own primary.
func primaryCheckoutRoot(repoRoot string) string {
	common := commonGitDir(repoRoot)
	if common == "" {
		return ""
	}
	dir := filepath.Clean(common)
	if strings.EqualFold(filepath.Base(dir), ".git") {
		dir = filepath.Dir(dir)
	}
	return dir
}

// MutantsTargetDir is that worktree's own persistent build directory. It is
// inside the worktree deliberately: the build-slot bypass is keyed on exactly
// that containment, so a target dir anywhere else queues like every other
// build. It is named `target` because that is the name every Rust repo
// already ignores — an untracked directory the repo does NOT ignore would
// make the run's own worktree read as dirty.
func MutantsTargetDir(repoRoot string) string {
	return filepath.Join(MutantsWorktreeDir(repoRoot), "target")
}

// mutantsRunDir holds the job's own output inside that build directory.
const mutantsRunDir = "aphrollo-mutants"

// MutantsArgv is cargo-mutants' own flags for a lane run: mutate the warm
// worktree IN PLACE (never a tree copy), only inside the lane's diff, and run
// the suite through nextest. baselineSkip drops the unmutated baseline run,
// which is sound only when the gate already proved that same tree green.
// packages narrows both the mutant pool and — the point of issue #251 — the
// unmutated BASELINE to the touched crates' own suites, via mutantsTouchedPackages;
// empty falls back to today's whole-workspace scope rather than measuring
// nothing. judged names the mutants an interrupted earlier attempt already
// reached a verdict for; they are excluded so a restart measures only what is
// left.
func MutantsArgv(diffPath string, baselineSkip bool, judged []string, packages []string) []string {
	argv := []string{"--in-place", "--in-diff", diffPath, "--test-tool=nextest"}
	if baselineSkip {
		argv = append(argv, "--baseline", "skip")
	}
	for _, pkg := range packages {
		argv = append(argv, "--package", pkg)
	}
	// Every exclusion rides to the runner inside one environment variable, and
	// Windows caps the whole environment block at 32,767 characters: an
	// unbounded list of a few thousand judged mutants truncates the run's own
	// arguments. So the list is bounded, and the overflow is simply
	// re-measured — dropping an exclusion costs one mutant's runtime, dropping
	// the arguments costs the run.
	size := len(strings.Join(argv, " "))
	for _, name := range judged {
		re := "^" + regexp.QuoteMeta(name) + "$"
		cost := len("--exclude-re ") + len(re) + 1
		if size+cost > mutantsArgvBudget {
			break
		}
		argv = append(argv, "--exclude-re", re)
		size += cost
	}
	return argv
}

// mutantsArgvBudget is how long the runner's argument string may get. Well
// under the 32,767-character Windows environment block, because the block
// holds the rest of the environment too.
const mutantsArgvBudget = 8000

// TipSuiteGreen reports whether the gate's own log shows this checkout's suite
// green since `since` — the proof that lets a run skip its baseline.
func TipSuiteGreen(root string, since time.Time) bool {
	path := GateLogPath()
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	return tipSuiteGreen(f, root, since)
}

// tipSuiteGreen judges the LAST commit-gate run for root inside the window.
// The last one, not any one: a green followed by a timeout means the suite's
// most recent word was "unknown", and a baseline skipped on that would report
// every mutant caught by a suite that never ran.
func tipSuiteGreen(r io.Reader, root string, since time.Time) bool {
	last := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || e.stage != "precommit" || e.root != root || e.at.Before(since) {
			continue
		}
		last = e.verdict
	}
	return last == "green"
}

// mutantsStateDir holds the job records, lane diffs and run logs, beside the
// gate's other state and never in the repo.
func mutantsStateDir() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	dir = filepath.Join(dir, "mutants")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// mutantsJobsPath is the record of one repo's jobs. Keyed by the repo, not by
// the worktree: every lane of a repo shares one mutation worktree and one
// registry.
func mutantsJobsPath(repo string) string {
	dir := mutantsStateDir()
	if dir == "" || repo == "" {
		return ""
	}
	return filepath.Join(dir, "jobs."+projectKey(repo)+".json")
}

// saveMutantsJob APPENDS a job to its repo's registry, dropping the entries
// that are over. It never removes a live one: superseding a run is not
// cancelling it.
func saveMutantsJob(j MutantsJob) {
	path := mutantsJobsPath(j.Repo)
	if path == "" {
		return
	}
	jobs := append(RunningMutantsJobs(j.Repo), j)
	data, err := json.Marshal(jobs)
	if err != nil {
		return
	}
	_ = writeFileAtomic(path, data)
}

// RunningMutantsJobs is every job for repo whose process is still alive and
// which has not been going long enough to be a wedge, newest first.
func RunningMutantsJobs(repo string) []MutantsJob {
	path := mutantsJobsPath(repo)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var jobs []MutantsJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil
	}
	now := time.Now()
	var live []MutantsJob
	for _, j := range jobs {
		if j.PID > 0 && now.Sub(j.Started) < mutantsJobMaxAge && jobProcessLives(j) {
			live = append(live, j)
		}
	}
	return live
}

// jobProcessLives reports whether the process this job recorded is the one
// running under that pid now. A record with no token is judged by pid alone:
// the alternative is reporting every job of an older binary as finished, which
// is the wrong direction for a gate that refuses on a missing receipt.
func jobProcessLives(j MutantsJob) bool {
	if !pidRunningFn(j.PID) {
		return false
	}
	if j.PIDStart == "" {
		return true
	}
	now := processStartTokenFn(j.PID)
	return now == "" || now == j.PIDStart
}

// MutantsJobRunningAt reports the job running for the repo this directory
// belongs to, for the statusline badge.
func MutantsJobRunningAt(dir string) (MutantsJob, bool) {
	repo := commonGitDir(dir)
	if repo == "" {
		return MutantsJob{}, false
	}
	jobs := RunningMutantsJobs(repo)
	if len(jobs) == 0 {
		return MutantsJob{}, false
	}
	return jobs[len(jobs)-1], true
}

// pidRunningFn is the liveness probe, a seam so a test can describe a dead
// process without having to produce one.
var pidRunningFn = pidRunning

// processStartTokenFn is the start-time probe, a seam for the tests that
// describe a recycled pid without waiting for one.
var processStartTokenFn = processStartToken

// PostCommitHook is everything the one post-commit hook does, in the order it
// must do it. Two features landed on the same hook and git runs exactly one:
// the gate NOTE that lets CI tell a red on a gated tip from a red on an
// ungated one, and the lane's mutation run.
//
// The note goes first because it describes a commit that already exists and
// costs milliseconds; the run goes second because it outlives this process.
// Neither half can block — by the time this runs, the commit is made.
func PostCommitHook(repoRoot string) (MutantsJob, bool, error) {
	PostCommit(repoRoot)
	return startMutantsJob(repoRoot)
}
