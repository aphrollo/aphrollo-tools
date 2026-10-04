package cli

import (
	"os"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// gateLogBytes is the gate's stage lines as the retired gate.log kept them,
// rendered from the event logs; an error when none was recorded, as the read of
// a gate.log that was never written was.
func gateLogBytes(t *testing.T) ([]byte, error) {
	t.Helper()
	text := tdd.GateLines(time.Time{})
	if text == "" {
		return nil, os.ErrNotExist
	}
	return []byte(text), nil
}
