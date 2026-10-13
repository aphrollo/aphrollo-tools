package workspace

import (
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// laneIsOff is the seam over whether the lane at wt is in the off arm of the on/off
// comparison (tddarm.ArmOff): a package var so merge tests need no repo and no config.
var laneIsOff = func(wt string) bool { return tdd.EffectiveTDD(wt).Off() }
