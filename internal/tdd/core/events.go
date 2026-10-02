package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EventSchema is the version every record of events.jsonl carries. The log is
// append-only and read by binaries of different ages, so the version rides in
// each line, and a reader skips a line whose version it does not know.
const EventSchema = 1

// Event is one line of <stateDir>/events.jsonl, the machine-readable trail the
// targets (escapes per merged PR, pushes per PR, CI green on first run, wrong
// denies) are measured from. It carries metadata only: never file contents,
// never a tool's input text, and never the command line gate.log records.
type Event struct {
	V       int     `json:"v"`
	At      string  `json:"at"` // UTC RFC3339
	Kind    string  `json:"kind"`
	Repo    string  `json:"repo,omitempty"`
	Lane    string  `json:"lane,omitempty"`
	Stage   string  `json:"stage,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Secs    float64 `json:"secs,omitempty"`
	// Detail holds small identifiers a kind needs (a PR number, an escape id).
	Detail map[string]string `json:"detail,omitempty"`
	// Root is where the event happened; AppendEvent turns it into Repo and Lane
	// and never writes it.
	Root string `json:"-"`
}

// EventLogPath is events.jsonl beside gate.log, "" when there is no state dir.
func EventLogPath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "events.jsonl")
}

// AppendEvent writes one record. The line goes out in a single write on an
// O_APPEND handle, so concurrent hooks never interleave. Best-effort like
// gate.log: a failure never affects a gate decision.
func AppendEvent(e Event) {
	path := EventLogPath()
	if path == "" {
		return
	}
	e.V = EventSchema
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339)
	}
	e.Repo, e.Lane = repoAndLane(e.Root)
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// ReadEvents returns every record this binary understands, in file order. A
// line of another version, or one that does not parse, is skipped: the file
// is append-only and shared with other binaries.
func ReadEvents() []Event {
	path := EventLogPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var e Event
		if json.Unmarshal(line, &e) == nil && e.V == EventSchema {
			out = append(out, e)
		}
		if err != nil {
			return out
		}
	}
}

// eventKind names what a gate.log line was, from its stage and verdict.
func eventKind(stage, verdict string) string {
	switch stage {
	case "precommit":
		return "commit_gate"
	case premergeDisplayName, premergeLogToken:
		return "merge_gate"
	case "commitmsg":
		return "commit_msg"
	case "mutants":
		return "mutants"
	}
	if strings.HasPrefix(verdict, "pretooluse-denied") || (stage == "git" && strings.Contains(verdict, "-refused")) {
		return "deny"
	}
	if stage == "postedit" || stage == "preedit" {
		return "edit"
	}
	return "gate"
}

// repoAndLane resolves the main checkout and the branch of root by reading
// .git directly: a gate line is logged on every edit, too often to spawn git.
// Both are "" for a root that is not a path inside a repo.
func repoAndLane(root string) (repo, lane string) {
	if root == "" || root == "-" {
		return "", ""
	}
	dir := filepath.FromSlash(root)
	for {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if fi.IsDir() {
				return filepath.ToSlash(dir), headBranch(gitPath)
			}
			return linkedRepoAndLane(dir, gitPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return root, ""
		}
		dir = parent
	}
}

func linkedRepoAndLane(dir, gitFile string) (string, string) {
	data, err := os.ReadFile(gitFile)
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if err != nil || !ok {
		return filepath.ToSlash(dir), ""
	}
	gitdir = filepath.FromSlash(strings.TrimSpace(gitdir))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	lane := headBranch(gitdir)
	common := gitdir
	if rel, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = filepath.Join(gitdir, strings.TrimSpace(string(rel)))
	}
	return filepath.ToSlash(filepath.Dir(filepath.Clean(common))), lane
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
