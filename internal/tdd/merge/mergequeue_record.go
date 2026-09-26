package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A `workspace merge --wait <pr>...` queue runs in the process that started
// it, so it dies with that process's session. While it runs it keeps a record
// here, one per repository: the PRs in order with each one's status, the
// queue's pid, its start and its repo. The queue removes the record once no
// PR is left pending; a record still present whose pid is gone is a queue
// that stopped, and the next session start in that repository reports it.

// A PR's status in a queue record.
const (
	MergeQueuePending = "pending"
	MergeQueueMerged  = "merged"
	MergeQueueRefused = "refused"
)

// MergeQueuePR is one PR of a queue record and where it stands.
type MergeQueuePR struct {
	PR     int    `json:"pr"`
	Status string `json:"status"`
}

// MergeQueueRecord is a merge queue as it stands on disk.
type MergeQueueRecord struct {
	Repo    string         `json:"repo"`
	PID     int            `json:"pid"`
	Started time.Time      `json:"started"`
	PRs     []MergeQueuePR `json:"prs"`
}

// mergeQueueRecordPath names repo's record. A lane and its primary checkout
// share one record; a path git does not know keys by itself.
func mergeQueueRecordPath(repo string) string {
	root := primaryCheckoutRoot(repo)
	if root == "" {
		root = repo
		if abs, err := filepath.Abs(repo); err == nil {
			root = abs
		}
	}
	return filepath.Join(StateDir(), "merge-queue", retroSlug(filepath.Clean(root))+".json")
}

// SaveMergeQueueRecord writes r under its repo, replacing any earlier record.
func SaveMergeQueueRecord(r *MergeQueueRecord) error {
	path := mergeQueueRecordPath(r.Repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// LoadMergeQueueRecord reads repo's record; nil with no error when there is none.
func LoadMergeQueueRecord(repo string) (*MergeQueueRecord, error) {
	data, err := os.ReadFile(mergeQueueRecordPath(repo))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // absence-ok: no queue has run here, or the last one finished
	}
	if err != nil {
		return nil, err
	}
	var r MergeQueueRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("merge queue record for %s: %w", repo, err)
	}
	return &r, nil
}

// RemoveMergeQueueRecord deletes repo's record, best-effort: the queue saves
// its last state first, and a record with no PR pending is never reported nor
// resumed, so one left behind is inert.
func RemoveMergeQueueRecord(repo string) {
	_ = os.Remove(mergeQueueRecordPath(repo))
}

// Live reports whether the queue's process still runs.
func (r *MergeQueueRecord) Live() bool {
	return pidRunningFn(r.PID)
}

// Pending lists the PRs not yet merged or refused, in queue order.
func (r *MergeQueueRecord) Pending() []int {
	var out []int
	for _, p := range r.PRs {
		if p.Status == MergeQueuePending {
			out = append(out, p.PR)
		}
	}
	return out
}

// Set records pr's status.
func (r *MergeQueueRecord) Set(pr int, status string) {
	for i := range r.PRs {
		if r.PRs[i].PR == pr {
			r.PRs[i].Status = status
		}
	}
}

// StoppedLine is the report for a queue whose process is gone: every PR it
// held and the first one it left pending. "" when none is pending.
func (r *MergeQueueRecord) StoppedLine() string {
	pending := r.Pending()
	if len(pending) == 0 {
		return ""
	}
	all := make([]string, 0, len(r.PRs))
	for _, p := range r.PRs {
		all = append(all, fmt.Sprintf("#%d", p.PR))
	}
	return fmt.Sprintf("merge queue for %s stopped at #%d (process gone) — resume: aphrollo workspace merge --wait --resume",
		strings.Join(all, " "), pending[0])
}

// MergeQueueStoppedLine reports dir's repository's merge queue when its
// process is gone; "" when there is no record or the queue still runs.
func MergeQueueStoppedLine(dir string) string {
	r, err := LoadMergeQueueRecord(dir)
	if err != nil || r == nil || r.Live() {
		return ""
	}
	return r.StoppedLine()
}
