package report

import (
	"math"
	"testing"
)

// The exact p values are the two-sided tail counts of the Mann-Whitney U
// distribution, checked against the textbook critical-value table for the
// two-sided 0.05 level (Mann and Whitney 1947; Siegel and Castellan, Table):
// the largest significant U is 0 for n 4 and 4, 1 for 4 and 5, 2 for 5 and 5.
// P(U=k) for (4,4) starts 1,1,2,3 of C(8,4)=70; for (5,5) 1,1,2,3 of 252; for
// (4,5) 1,1,2,3 of 126.
func TestMannWhitneyP_ExactTwoSidedAgainstTheCriticalValueTable(t *testing.T) {
	cases := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"4,4 U=0: 2 of 70", []float64{1, 2, 3, 4}, []float64{5, 6, 7, 8}, 2.0 / 70},
		{"4,4 U=1: 4 of 70", []float64{1, 2, 3, 5}, []float64{4, 6, 7, 8}, 4.0 / 70},
		{"5,5 U=2: 8 of 252", []float64{1, 2, 3, 4, 7}, []float64{5, 6, 8, 9, 10}, 8.0 / 252},
		{"4,5 U=1: 4 of 126", []float64{1, 2, 3, 5}, []float64{4, 6, 7, 8, 9}, 4.0 / 126},
		{"4,5 U=2: 8 of 126", []float64{1, 2, 3, 6}, []float64{4, 5, 7, 8, 9}, 8.0 / 126},
		{"order of the arguments does not matter", []float64{8, 7, 6, 5}, []float64{4, 3, 2, 1}, 2.0 / 70},
	}
	for _, c := range cases {
		if got := mannWhitneyP(c.a, c.b); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s: p = %.10f, want %.10f", c.name, got, c.want)
		}
	}
}

// Hand computation: ranks of 1,2,2,2,3,3,3,4 are 1,3,3,3,6,6,6,8; sample a
// (1,2,2,3) has rank sum 13 so U=3 against a mean of 8; the tie term is
// (27-3)+(27-3)=48, so the variance is 16/12*(9-48/56)=10.857 and, with the
// continuity correction, z=(5-0.5)/3.295=1.3657, two-sided p=0.172.
func TestMannWhitneyP_TiesUseTheNormalApproximationWithTheTieCorrection(t *testing.T) {
	got := mannWhitneyP([]float64{1, 2, 2, 3}, []float64{2, 3, 3, 4})
	if math.Abs(got-0.172) > 0.002 {
		t.Errorf("p = %.4f, want 0.172", got)
	}
}

func TestMannWhitneyP_AllValuesEqualIsNoDifference(t *testing.T) {
	if got := mannWhitneyP([]float64{5, 5, 5, 5}, []float64{5, 5, 5, 5}); got != 1 {
		t.Errorf("p = %v, want 1", got)
	}
}

// 51 and 51 samples that do not overlap: U=0, mean 1300.5, sd sqrt(51*51*103/12)
// = 149.4, z = 1300/149.4 = 8.70, p about 3e-18.
func TestMannWhitneyP_LargeSamplesUseTheNormalApproximation(t *testing.T) {
	var a, b []float64
	for i := 0; i < 51; i++ {
		a = append(a, float64(i))
		b = append(b, float64(100+i))
	}
	if got := mannWhitneyP(a, b); got <= 0 || got > 1e-12 {
		t.Errorf("p = %g, want a tiny positive value", got)
	}
}

func TestMannWhitneyP_SameInputSameBytes(t *testing.T) {
	a, b := []float64{3, 1, 4, 1, 5}, []float64{9, 2, 6, 5, 3}
	if x, y := mannWhitneyP(a, b), mannWhitneyP(b, a); x != y {
		t.Errorf("p differs with the sides swapped: %v %v", x, y)
	}
}

func TestClearChange_NeedsFourRunsOnEachSideAndPUnderOneTwentieth(t *testing.T) {
	sep4a, sep4b := []float64{1, 2, 3, 4}, []float64{5, 6, 7, 8}
	if p, ok := clearChange(sep4a, sep4b); !ok || math.Abs(p-2.0/70) > 1e-12 {
		t.Errorf("4 and 4 apart: p %v ok %v, want 2/70 and clear", p, ok)
	}
	if _, ok := clearChange([]float64{1, 2, 3}, []float64{5, 6, 7, 8}); ok {
		t.Error("3 runs on one side is never a clear change")
	}
	if _, ok := clearChange([]float64{1, 2, 3, 5}, []float64{4, 6, 7, 8}); ok {
		t.Error("p 4/70 = 0.057 is not under 0.05")
	}
}
