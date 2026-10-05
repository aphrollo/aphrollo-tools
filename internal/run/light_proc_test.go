//go:build proc

package run

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The process tier: timeouts of 8 and 10 seconds over real process trees, run
// nightly with -tags proc. The default tier keeps the cancel variant of the
// same tree kill and the short-timeout variants of the same errors.

// A light child that times out takes its whole tree with it, MSYS
// grandchildren included: on Windows walking the tree from the pid missed them
// under load and left them running after the timeout.
func TestLightRun_TimeoutEndsTheWholeTree(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			spec := ch.spec(t, pidFile, false)
			spec.Timeout = 10 * time.Second

			err := LightRun(spec)

			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}
			pids := readPids(pidFile)
			watch(t, pids)
			if len(pids) < ch.pids {
				t.Fatalf("the tree recorded %d pids before the timeout, want %d: it never came up", len(pids), ch.pids)
			}
			assertTreeGone(t, pids)
		})
	}
}

func TestHeavyRun_TimeoutEndsTheWholeTreeAndSaysTheChildGaveNoAnswer(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			spec := ch.spec(t, pidFile, false)
			spec.Timeout = 8 * time.Second

			err := HeavyRun(spec)

			pids := readPids(pidFile)
			watch(t, pids)
			if err == nil || !strings.Contains(err.Error(), "no answer within 8s") {
				t.Fatalf("err = %v, want it to say the child gave no answer within 8s and was killed", err)
			}
			if len(pids) < ch.pids {
				t.Fatalf("the tree recorded %d pids, want %d: it never came up", len(pids), ch.pids)
			}
			assertTreeGone(t, pids)
		})
	}
}
