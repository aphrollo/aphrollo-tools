package tdd

import (
	"fmt"
	"time"
)

// formatElapsedSecs renders a duration the way the rest of gate status's
// report does — whole seconds, never negative (a clock read racing a
// just-started job must not print "-1s").
func formatElapsedSecs(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
}
