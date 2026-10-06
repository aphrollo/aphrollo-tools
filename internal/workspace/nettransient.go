package workspace

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// A wait reads GitHub for up to an hour and a half, so some read in it will meet
// a dropped connection. That says nothing about the PR it was asking after: the
// wait retries, backing off, and gives up only after maxNetFailures failures in
// a row, then says the PR's state is unknown.
const (
	maxNetFailures = 5
	netRetryBase   = 5 * time.Second
)

// netFailureMarks are the spellings of a read that failed to reach GitHub or
// was cut off on the way, in gh's, Go's and Windows's own words. The errors come
// back flattened to text, so the text is what is read.
var netFailureMarks = []string{
	"dial tcp",
	"connectex",
	"i/o timeout",
	"connection reset",
	"connection refused",
	"connection aborted",
	"forcibly closed",
	"tls handshake timeout",
	"no such host",
	"temporary failure in name resolution",
	"network is unreachable",
	"unexpected eof",
	"timed out after",
	"context deadline exceeded",
	"http 502",
	"http 503",
	"http 504",
}

// isTransientNetError reports whether err is a failure to reach GitHub, which a
// later read may not meet, and not an answer GitHub gave (a 4xx, a missing PR).
func isTransientNetError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, mark := range netFailureMarks {
		if strings.Contains(msg, mark) {
			return true
		}
	}
	return false
}

// netRetry counts the network failures of a wait's status reads in a row.
type netRetry struct{ fails int }

// reached records a read that got an answer.
func (r *netRetry) reached() { r.fails = 0 }

// failed records one read that did not. It answers how long to wait before the
// next read, or, once maxNetFailures reads in a row have failed, the error that
// ends the wait. A failure that is not a network error is returned as it is.
func (r *netRetry) failed(err error, what, ref string, interval time.Duration, stdout io.Writer) (time.Duration, error) {
	if !isTransientNetError(err) {
		return 0, err
	}
	r.fails++
	if r.fails >= maxNetFailures {
		return 0, fmt.Errorf("the state of %s is unknown, not 'not merged': GitHub could not be reached %d times in a row (last: %v) — "+
			"check it with `gh pr view %s` before merging again", what, r.fails, err, ref)
	}
	wait := netRetryBase << (r.fails - 1)
	if interval > 0 && wait > interval {
		wait = interval
	}
	fmt.Fprintf(stdout, "  [wait] %s: GitHub could not be reached (%d of %d tries): %v; trying again in %v\n", what, r.fails, maxNetFailures, err, wait)
	return wait, nil
}

// pause is failed followed by the wait it asks for, which the wait's own
// timeout still bounds. A nil answer means read again.
func (r *netRetry) pause(err error, what, ref string, o WaitOpts, deadline time.Time, stdout io.Writer) error {
	wait, err := r.failed(err, what, ref, o.Interval, stdout)
	if err != nil {
		return err
	}
	if !waitNow().Add(wait).Before(deadline) {
		return fmt.Errorf("timed out after %v waiting for %s (last: GitHub could not be reached)", o.Timeout, what)
	}
	waitSleep(wait)
	return nil
}
