package tdd

import "time"

// GateLines is the box's gate stage lines since since (the zero time: all) as
// text, one per line and oldest first, rendered from the event logs. The gate
// reports (`gate stats`, the demotion and override scans) read it.
func GateLines(since time.Time) string { return gateLinesSince(since) }
