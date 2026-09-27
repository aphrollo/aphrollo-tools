package workspace

import (
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A merge queue runs in the process that started it and dies with it. It is
// never detached: it keeps a record in the gate's state dir instead, so the
// next session start in the repository reports a queue whose process is gone,
// and `merge --wait --resume` finishes the PRs it left.

// queueNames renders PR numbers as "#a #b".
func queueNames(prs []int) string {
	names := make([]string, 0, len(prs))
	for _, n := range prs {
		names = append(names, fmt.Sprintf("#%d", n))
	}
	return strings.Join(names, " ")
}

// refuseLiveQueue refuses when a queue whose process still runs holds the
// repository: two queues merging into one base race each other.
func refuseLiveQueue(mainRepo string) error {
	held, err := tdd.LoadMergeQueueRecord(mainRepo)
	if err != nil || held == nil || !held.Live() {
		return nil
	}
	return fmt.Errorf("a merge queue for %s already runs as pid %d (%s); wait for it to finish", mainRepo, held.PID, queueNames(held.Pending()))
}

// claimQueueRecord writes the record of a queue about to run items: what a
// resumed queue's prior record already settled, then items, all pending. A
// record it cannot write is reported and the queue still runs.
func claimQueueRecord(mainRepo string, prior *tdd.MergeQueueRecord, items []QueueItem, stderr io.Writer) (*tdd.MergeQueueRecord, error) {
	if err := refuseLiveQueue(mainRepo); err != nil {
		return nil, err
	}
	rec := &tdd.MergeQueueRecord{Repo: mainRepo, Started: waitNow()}
	rec.StampThisProcess()
	if prior != nil {
		for _, p := range prior.PRs {
			if p.Status != tdd.MergeQueuePending {
				rec.PRs = append(rec.PRs, p)
			}
		}
	}
	for _, it := range items {
		rec.PRs = append(rec.PRs, tdd.MergeQueuePR{PR: it.PR, Status: tdd.MergeQueuePending})
	}
	saveQueueRecord(rec, stderr)
	return rec, nil
}

// settleQueuePR records pr's status, and removes the record once no PR is
// left pending: the queue is finished.
func settleQueuePR(rec *tdd.MergeQueueRecord, pr int, status string, stderr io.Writer) {
	rec.Set(pr, status)
	saveQueueRecord(rec, stderr)
	if len(rec.Pending()) == 0 {
		tdd.RemoveMergeQueueRecord(rec.Repo)
	}
}

func saveQueueRecord(rec *tdd.MergeQueueRecord, stderr io.Writer) {
	if err := tdd.SaveMergeQueueRecord(rec); err != nil {
		fmt.Fprintf(stderr, "aphrollo: merge queue record not written, so a stop will not be reported: %v\n", err)
	}
}

// PlanResume reloads the repository's stopped queue and plans the PRs it left
// pending. It refuses when there is none, or when its process still runs.
func PlanResume(mainRepo string) (*tdd.MergeQueueRecord, []QueueItem, error) {
	prior, err := tdd.LoadMergeQueueRecord(mainRepo)
	if err != nil {
		return nil, nil, err
	}
	if prior == nil || len(prior.Pending()) == 0 {
		return nil, nil, fmt.Errorf("no stopped merge queue to resume for %s", mainRepo)
	}
	if err := refuseLiveQueue(mainRepo); err != nil {
		return nil, nil, err
	}
	items, err := PlanMergeQueue(mainRepo, prior.Pending())
	if err != nil {
		return nil, nil, err
	}
	return prior, items, nil
}

// ResumeMergeQueue runs the PRs PlanResume planned, keeping in the record
// what the stopped queue already settled. A nil prior runs items as a fresh
// queue, exactly as RunMergeQueue does.
func ResumeMergeQueue(mainRepo string, prior *tdd.MergeQueueRecord, items []QueueItem, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	return runQueue(mainRepo, prior, items, method, deleteBranch, o, stdout, stderr)
}
