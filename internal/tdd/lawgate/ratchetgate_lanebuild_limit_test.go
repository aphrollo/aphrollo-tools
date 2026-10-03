package lawgate

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest/chaintool"
)

// shrinkLaneBuildLimit gives the lane's children a limit short enough to wait
// out, and puts the old one back.
func shrinkLaneBuildLimit(t *testing.T, d time.Duration) {
	t.Helper()
	prev := laneBuildTimeout
	laneBuildTimeout = d
	t.Cleanup(func() { laneBuildTimeout = prev })
}

// A build that hangs refuses the commit with a reason, and the shell chain it
// started goes with it: a build left compiling would hold the box's memory
// after the gate gave up.
func TestLaneFixtureBuild_LimitEndsTheBuildsWholeTree(t *testing.T) {
	pidFile, _ := chaintool.Install(t, "go")
	shrinkLaneBuildLimit(t, 8*time.Second)

	_, cleanup, err := laneFixtureBuild(t.TempDir())
	if cleanup != nil {
		defer cleanup()
	}

	if err == nil || !strings.Contains(err.Error(), "go build -o") {
		t.Fatalf("err = %v, want the build's refusal naming the go build", err)
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}

func TestLaneFixtureRun_LimitEndsTheLanesBinarysWholeTree(t *testing.T) {
	pidFile, tool := chaintool.Install(t, "lane-aphrollo")
	shrinkLaneBuildLimit(t, 8*time.Second)

	_, err := laneFixtureRun(tool, t.TempDir(), []string{"module_size"})

	if err == nil || !strings.Contains(err.Error(), "ratchet test") {
		t.Fatalf("err = %v, want the lane binary's refusal naming its ratchet test", err)
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}
