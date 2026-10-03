package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// TestMain cuts the package's run off from the box's git world: the
// repositories around it, the environment a hook exports, and the operator's
// git config. See gitiso.Isolate. The two-process tests start this binary again
// with writerDirEnv set, and that run is a writer, not a test run.
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int {
		if os.Getenv(writerDirEnv) != "" {
			return writerMain()
		}
		return m.Run()
	}))
}

const (
	writerDirEnv   = "STORE_TEST_WRITER_DIR"
	writerIDEnv    = "STORE_TEST_WRITER_ID"
	writerCrashEnv = "STORE_TEST_WRITER_CRASH_AT"
	writesPerProc  = 25
)

// writerMain adds writesPerProc actors to lane "fix" the way the engine does,
// loading, deciding, committing, and starting over on a lost update. The
// crash variable names the write whose process dies between the log append and
// the checkpoint replace. It returns the exit code.
func writerMain() int {
	id := os.Getenv(writerIDEnv)
	crashAt, _ := strconv.Atoi(os.Getenv(writerCrashEnv))
	s, err := Open(os.Getenv(writerDirEnv), Options{LockWait: 2 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for i := range writesPerProc {
		ev := entered("fix", fmt.Sprintf("p%s/%d", id, i))
		if crashAt > 0 && i+1 == crashAt {
			s.afterAppend = func() error { os.Exit(3); return nil }
		}
		for {
			_, err := decideCommit(ctx, s, ev)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrConflict) {
				fmt.Fprintf(os.Stderr, "write %d of writer %s: %v\n", i, id, err)
				return 1
			}
		}
	}
	return 0
}
