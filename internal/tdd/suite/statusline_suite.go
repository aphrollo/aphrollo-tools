package suite

import (
	"time"
)

// redGoesStaleAfter bounds how long a recorded red may speak for the tree with
// nothing after it. The badge is a real-time signal or it is noise: a red from
// an hour ago describes code the session has long since moved past, and one
// false red teaches a reader to ignore the true one. Nothing is rendered in
// its place -- a `stale` word would be the same false claim, spelled longer.
const redGoesStaleAfter = 30 * time.Minute
