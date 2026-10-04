package core

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EventSchema is the version every record of the event logs carries. The logs
// are append-only and read by binaries of different ages, so the version rides
// in each line, and a reader skips a line whose version it does not know.
const EventSchema = 1

// Event is one line of <StateRoot>/state/<repo>/events-YYYY-MM.jsonl, the
// machine-readable trail the targets (escapes per merged PR, pushes per PR, CI
// green on first run, wrong denies, not-tested runs, edit-to-verdict latency)
// are measured from. It carries metadata only: never file contents, never a
// tool's input text. A gate stage line is the one exception: it keeps the
// invocation (Cmd) and the root it ran in, as gate.log's line did, except the
// command of a deny or an override, which is what someone typed.
//
// Kinds in use: stage lines by stage (commit_gate, merge_gate, commit_msg,
// mutants, stage.timing, gate and the *_result of a whole run), deny, override,
// run.result, edit, hook.timing, feedback, escape, ci, merge, pr_opened, push.
type Event struct {
	V int `json:"v"`
	// Seq orders a repo's events: the writer numbers each record under the
	// log's lock from the file's size, a record written without the lock
	// carries none on disk, and ReadEvents numbers every record from its place
	// in the file whatever was written, so a Seq read from a month file is
	// never 0 and never shared.
	Seq     int64   `json:"seq,omitempty"`
	At      string  `json:"at"` // UTC RFC3339, millisecond precision
	Lane    string  `json:"lane,omitempty"`
	Actor   string  `json:"actor,omitempty"` // session id, or session/agent id
	Kind    string  `json:"kind"`
	Repo    string  `json:"repo,omitempty"`
	Stage   string  `json:"stage,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Secs    float64 `json:"secs,omitempty"`
	// Detail holds small identifiers a kind needs (a PR number, an escape id).
	Detail map[string]string `json:"detail,omitempty"`
	// Cmd is the invocation a stage line records ("" for an event that is none).
	// It is what the scope law and the stage-duration floor match a recorded
	// run by, so the event of a gate line carries it as gate.log's line did.
	Cmd string `json:"cmd,omitempty"`
	// Root is where the event happened; AppendEvent turns it into Repo and Lane.
	// A Lane the caller already knows (an escape recorded from the main
	// checkout, naming its PR's lane) is kept over the branch the root has
	// checked out. The event of a gate stage line keeps the root itself, which
	// is what the readers of a worktree's own stages match on (a worktree is a
	// project of its own, the repo is not); every other event drops it.
	Root string `json:"root,omitempty"`
}

// AppendEvent writes one record to the log of the repository Root belongs to.
// The line goes out in a single write under the log's lock, so concurrent hooks
// never interleave. Best-effort like gate.log: a failure never affects a gate
// decision, but it is said once.
func AppendEvent(e Event) {
	e.V = EventSchema
	if e.At == "" {
		e.At = time.Now().UTC().Format(eventTimeFormat)
	}
	repo, lane, common := repoIdentity(e.Root)
	e.Repo = repo
	if e.Lane == "" {
		e.Lane = lane
	}
	if e.Stage == "" {
		e.Root = ""
	}
	if e.Actor == "" {
		e.Actor = SessionID()
	}
	dir := RepoStateDir(common)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		warnEventLogUnwritable(fmt.Sprintf("could not create %s: %v", dir, err))
		return
	}
	path := eventLogFile(dir, monthOf(e.At))
	if err := appendEventRecord(path, e); err != nil {
		warnEventLogUnwritable(fmt.Sprintf("could not write %s: %v", path, err))
	}
}

// AppendEventOnce writes e unless a record of the same kind already carries the
// same Detail[key], and reports whether it wrote. A settled CI result is read
// by several verbs; the log keeps one record per commit. Only the last two
// months are searched: a commit is not settled again a month later.
func AppendEventOnce(e Event, key string) bool {
	want := e.Detail[key]
	for _, old := range readEventsRecent(e.Root) {
		if old.Kind == e.Kind && old.Detail[key] == want {
			return false
		}
	}
	AppendEvent(e)
	return true
}

var eventLogWarnOnce sync.Once

func resetEventLogWarnForTest() { eventLogWarnOnce = sync.Once{} }

// warnEventLogUnwritable is the one place a lost event becomes visible, once
// per process: every target is computed from these files, so a log that quietly
// stops growing would read as a quiet week.
func warnEventLogUnwritable(reason string) {
	eventLogWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "aphrollo gate: the event log is not being written: %s\n", reason)
	})
}

// eventKind names what a gate.log line was, from its stage and verdict. A
// refusal, a waiver and a run that proved nothing outrank the stage: they are
// what the measures count, and the line's stage and seconds stay on the event.
func eventKind(stage, verdict string) string {
	switch {
	case isDenyLine(stage, verdict):
		return "deny"
	case isOverrideVerdict(verdict):
		return "override"
	case notTestedCause(verdict) != "":
		return "run.result"
	}
	switch stage {
	case "precommit":
		return "commit_gate"
	case premergeDisplayName, premergeLogToken:
		return "merge_gate"
	case "commitmsg":
		return "commit_msg"
	case "mutants":
		return "mutants"
	case "postedit", "preedit":
		return "stage.timing"
	}
	return "gate"
}

func isDenyLine(stage, verdict string) bool {
	return strings.HasPrefix(verdict, "pretooluse-denied") || (stage == "git" && strings.Contains(verdict, "-refused"))
}

// cmdStages are the stages whose command is one the gate constructed itself (a
// suite, a build, a lint, a declared check, a fixed label): the readers of
// recorded runs match on it. Every other stage logs from text a user or an agent
// typed, or from a token the line only needs for display, and may hold a secret,
// so its command stays off the event. A stage is added here only when none of
// its call sites passes typed text.
var cmdStages = map[string]bool{"postedit": true, "precommit": true, premergeDisplayName: true, premergeLogToken: true, "mutants": true, "commitmsg": true}

// eventCmd is the command an event may carry: the gate's own, never one that
// was typed. A deny or an override line's command is whatever the user or the
// agent wrote, so those stay out whatever the stage.
func eventCmd(stage, kind, cmd string) string {
	if kind == "deny" || kind == "override" || !cmdStages[stage] {
		return ""
	}
	return cmd
}

func isOverrideVerdict(verdict string) bool {
	return strings.HasPrefix(verdict, "override-") || strings.HasPrefix(verdict, "smell-escape:")
}

// notTestedCause says why a run proved nothing, "" when the verdict is not one
// of the not-tested ones. The causes are the ones the gate line already names:
// timeout, skipped, queued, deferred, infra, no-tests.
func notTestedCause(verdict string) string {
	switch {
	case strings.HasPrefix(verdict, "timeout"), verdict == "lint-timeout":
		return "timeout"
	case strings.HasPrefix(verdict, "skipped"):
		return "skipped"
	case strings.HasPrefix(verdict, "queued-"):
		return "queued"
	case strings.HasPrefix(verdict, "deferred"):
		return "deferred"
	case verdict == "infra-failed":
		return "infra"
	case verdict == "no-tests-selected":
		return "no-tests"
	}
	return ""
}

// lineEventDetail is what an event of kind carries beyond its stage, verdict
// and seconds: the rule a deny came from, the waiver an override used, the
// cause of a not-tested run. A site that knows more (a deny's cause and the
// override it offered) passes it and wins.
func lineEventDetail(kind, verdict string, site map[string]string) map[string]string {
	detail := map[string]string{}
	switch kind {
	case "deny":
		rule, ok := strings.CutPrefix(verdict, "pretooluse-denied:")
		if !ok {
			rule = verdict
		}
		detail["rule"] = rule
	case "override":
		detail["override"] = verdict
	case "run.result":
		detail["result"] = "not-tested"
		detail["cause"] = notTestedCause(verdict)
	}
	maps.Copy(detail, site)
	if len(detail) == 0 {
		return nil
	}
	return detail
}

// repoIdentity resolves the main checkout, the branch and the git common dir
// of root by reading .git directly: a gate line is logged on every edit, too
// often to spawn git. repo and lane are "" for a root that is not a path inside
// a repo, and common is "" for any root that is no repository.
func repoIdentity(root string) (repo, lane, common string) {
	if root == "" || root == "-" {
		return "", "", ""
	}
	dir := filepath.FromSlash(root)
	for {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if fi.IsDir() {
				return filepath.ToSlash(dir), headBranch(gitPath), gitPath
			}
			return linkedRepoIdentity(dir, gitPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return root, "", ""
		}
		dir = parent
	}
}

func linkedRepoIdentity(dir, gitFile string) (repo, lane, common string) {
	data, err := os.ReadFile(gitFile)
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if err != nil || !ok {
		return filepath.ToSlash(dir), "", ""
	}
	gitdir = filepath.FromSlash(strings.TrimSpace(gitdir))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	lane = headBranch(gitdir)
	common = gitdir
	if rel, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = filepath.Join(gitdir, strings.TrimSpace(string(rel)))
	}
	common = filepath.Clean(common)
	return filepath.ToSlash(filepath.Dir(common)), lane, common
}

func headBranch(gitdir string) string {
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return ref
}
