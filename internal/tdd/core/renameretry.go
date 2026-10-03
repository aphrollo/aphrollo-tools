package core

import "time"

const (
	// renameBound is how long a rename that fails for a reason that can pass
	// is tried again before its error is returned.
	renameBound = time.Second
	// renameRetryEvery is the first pause between tries, doubled up to
	// renameRetryCap: a reader that polls in a tight loop leaves only short
	// gaps, which a slow poll rarely lands in.
	renameRetryEvery = time.Millisecond
	renameRetryCap   = 20 * time.Millisecond
)

// retryRename runs rename until it succeeds, fails with an error retryable
// rejects, or bound has passed, and answers the last error.
func retryRename(rename func() error, retryable func(error) bool, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	pause := renameRetryEvery
	for {
		err := rename()
		if err == nil || !retryable(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(pause)
		pause = min(pause*2, renameRetryCap)
	}
}
