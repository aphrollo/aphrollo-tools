package report

import (
	"math"
	"sort"
)

// Significance as Go's benchstat reads it: a change shows only when both sides
// have at least minClearRuns samples and a two-sided Mann-Whitney U test gives
// p under clearP. Nothing here is random: the same samples give the same p.
const (
	minClearRuns = 4
	clearP       = 0.05
	// exactMaxN is the largest sample for which the exact distribution is used;
	// above it, or with ties, the normal approximation stands in.
	exactMaxN = 50
)

// clearChange is the p value of a change between two samples and whether it is
// a clear one.
func clearChange(a, b []float64) (p float64, clear bool) {
	if len(a) < minClearRuns || len(b) < minClearRuns {
		return 1, false
	}
	p = mannWhitneyP(a, b)
	return p, p < clearP
}

// mannWhitneyP is the two-sided p value of the Mann-Whitney U test. Without ties
// and with both samples up to exactMaxN it is exact (the tail of the U
// distribution, counted); otherwise it is the normal approximation with the tie
// correction to the variance and a continuity correction.
func mannWhitneyP(a, b []float64) float64 {
	n1, n2 := len(a), len(b)
	if n1 == 0 || n2 == 0 {
		return 1
	}
	type obs struct {
		v     float64
		first bool
	}
	all := make([]obs, 0, n1+n2)
	for _, v := range a {
		all = append(all, obs{v, true})
	}
	for _, v := range b {
		all = append(all, obs{v, false})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].v < all[j].v })
	r1, tieTerm := 0.0, 0.0
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		mid := float64(i+j+1) / 2 // mean of ranks i+1..j
		for k := i; k < j; k++ {
			if all[k].first {
				r1 += mid
			}
		}
		t := float64(j - i)
		tieTerm += t*t*t - t
		i = j
	}
	f1, f2, n := float64(n1), float64(n2), float64(n1+n2)
	u1 := r1 - f1*(f1+1)/2
	u := math.Min(u1, f1*f2-u1)
	if tieTerm == 0 && n1 <= exactMaxN && n2 <= exactMaxN {
		return math.Min(1, 2*uTail(n1, n2, int(math.Round(u))))
	}
	variance := f1 * f2 / 12 * ((n + 1) - tieTerm/(n*(n-1)))
	if variance <= 0 {
		return 1
	}
	z := (math.Abs(u1-f1*f2/2) - 0.5) / math.Sqrt(variance)
	if z <= 0 {
		return 1
	}
	return math.Erfc(z / math.Sqrt2)
}

// uTail is P(U <= k) for samples of n1 and n2 with no ties, from the counts of
// the arrangements with each U, built by c(m,n,u) = c(m-1,n,u-n) + c(m,n-1,u).
func uTail(n1, n2, k int) float64 {
	size := n1*n2 + 1
	prev := make([][]float64, n2+1)
	cur := make([][]float64, n2+1)
	for n := range prev {
		prev[n], cur[n] = make([]float64, size), make([]float64, size)
		prev[n][0] = 1 // m = 0: one arrangement, U = 0
	}
	for m := 1; m <= n1; m++ {
		clear(cur[0])
		cur[0][0] = 1
		for n := 1; n <= n2; n++ {
			for u := 0; u < size; u++ {
				c := cur[n-1][u]
				if u >= n {
					c += prev[n][u-n]
				}
				cur[n][u] = c
			}
		}
		prev, cur = cur, prev
	}
	var total, tail float64
	for u, c := range prev[n2] {
		total += c
		if u <= k {
			tail += c
		}
	}
	return tail / total
}
