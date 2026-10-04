package host

import "fmt"

// Land lands the PR the way its base branch takes one: in the merge queue when
// the branch has one, else by a merge bound to the head the PR was judged at.
//
// A queue builds the PR onto the branch as it is at that moment and merges it
// when its own checks pass, so Land returns once the PR is in the queue
// (Landed.Queued) and leaves the waiting to the caller. The bind is the host's
// (the head goes to it with the enqueue); the head is read again right after,
// and a PR that moved anyway is taken back out and refused with a
// *HeadMovedError. A PR already in the queue (a resumed run) is left as it is.
func Land(h Host, r LandRequest) (Landed, error) {
	queued, err := h.HasMergeQueue(r.Repo, r.Base)
	if err != nil {
		return Landed{}, fmt.Errorf("reading whether %s has a merge queue: %w", r.Base, err)
	}
	if !queued {
		return Landed{}, h.Merge(MergeRequest{Branch: r.Branch, Method: r.Method, Head: r.Head,
			Subject: r.Subject, Body: r.Body, UseBody: r.UseBody})
	}
	if entry, err := h.QueueEntry(r.Repo, r.PR); err == nil && entry != nil {
		return Landed{Queued: true, Already: true, Entry: entry}, nil
	}
	if err := h.Enqueue(r.Repo, r.PR, r.Head); err != nil {
		return Landed{}, err
	}
	entry, entryErr := h.QueueEntry(r.Repo, r.PR)
	now, viewErr := h.PRByBranch(r.Branch)
	if viewErr != nil || now == nil {
		return Landed{}, fmt.Errorf("PR #%d was enqueued but its head could not be read back (%v), so it is in the queue at an unverified head: check it, and %s", r.PR, viewErr, h.DequeueHint(r.PR))
	}
	if now.HeadSHA != "" && now.HeadSHA != r.Head {
		msg := fmt.Sprintf("PR head moved to %s after it was judged and enqueued at %s", Short(now.HeadSHA), Short(r.Head))
		switch {
		case entryErr != nil || entry == nil:
			return Landed{}, &HeadMovedError{Msg: msg + fmt.Sprintf("; the queue entry could not be read, so it is STILL QUEUED at an unjudged head — %s", h.DequeueHint(r.PR))}
		case h.Dequeue(entry.ID) != nil:
			return Landed{}, &HeadMovedError{Msg: msg + fmt.Sprintf("; taking it out of the queue failed, so it is STILL QUEUED at an unjudged head — %s", h.DequeueHint(r.PR))}
		}
		return Landed{}, &HeadMovedError{Msg: msg + " — it was taken out of the queue; merge again to judge the new head"}
	}
	if entryErr != nil {
		entry = nil
	}
	return Landed{Queued: true, Entry: entry}, nil
}
