package ratchet

import "fmt"

// baseScopedKinds are the matchers whose hits a file's own bytes decide, so a
// base tree's hits can be measured the way the working tree's are. Whole-tree
// kinds answer from a graph, a registry or a diff and are judged as they were.
var baseScopedKinds = map[MatcherKind]bool{
	KindLineCount:         true,
	KindRegexAbsent:       true,
	KindPathRegexAbsent:   true,
	KindRegexPresent:      true,
	KindMarkerWithinLines: true,
	KindRegexNear:         true,
}

// baseHitsByLaw measures every base-scoped law over the tree the run's base
// names, keyed by law: the hits a checkout of the base would report. It is
// nil unless the run asked to be judged against its base, and it reads a
// path once for every law that claims it.
func baseHitsByLaw(opts Options, laws []Law) (map[string][]Hit, error) {
	if !opts.BaseRelative {
		return nil, nil
	}
	reader := resolveBaseTree(opts)
	if reader == nil {
		return nil, nil
	}
	var scoped []Law
	for _, l := range laws {
		if baseScopedKinds[l.Matcher.Kind] {
			scoped = append(scoped, l)
		}
	}
	all, err := reader.List()
	if err != nil {
		return nil, fmt.Errorf("base %s: %w", opts.Base, err)
	}
	narrowed := map[string]bool{}
	for _, rel := range opts.Files {
		narrowed[normalizeSlashes(rel)] = true
	}
	var want []string
	for _, rel := range all {
		if len(narrowed) > 0 && !narrowed[rel] {
			continue
		}
		if scopedByAny(scoped, rel) {
			want = append(want, rel)
		}
	}
	contents, err := reader.ReadAll(want)
	if err != nil {
		return nil, fmt.Errorf("base %s: %w", opts.Base, err)
	}
	out := map[string][]Hit{}
	for _, l := range scoped {
		out[l.Name] = nil
	}
	for _, rel := range want {
		data, ok := contents[rel]
		if !ok {
			continue
		}
		fl := newFileLines(rel, string(data))
		for _, l := range scoped {
			if l.Scope.Matches(rel) {
				out[l.Name] = append(out[l.Name], l.hitsInLines(rel, fl)...)
			}
		}
	}
	return out, nil
}

// baseCeiling is one law's base tree as a second ceiling: how many of each
// identity the base held, and how many of each literal site.
type baseCeiling struct {
	counts map[string]int
	sites  map[string]int
}

func newBaseCeiling(b *Baseline, hits []Hit) baseCeiling {
	c := baseCeiling{counts: map[string]int{}, sites: map[string]int{}}
	for _, h := range hits {
		c.counts[b.Identity(h.Key)] += h.Weight
		c.sites[h.Key]++
	}
	return c
}

// raise lifts each regression's ceiling to the base's count and keeps only
// those still above it: a hit the base already carried was not introduced.
func (c baseCeiling) raise(regs []Regression) []Regression {
	var out []Regression
	for _, r := range regs {
		r.Baseline = max(r.Baseline, c.counts[r.Key])
		if r.Measured > r.Baseline {
			out = append(out, r)
		}
	}
	return out
}

// literalKeys is the bag of sites a run treats as already accounted for: the
// baseline's rows and the base's sites, each site at the higher of the two.
func (c baseCeiling) literalKeys(baselineKeys map[string]int) map[string]int {
	out := map[string]int{}
	for k, n := range baselineKeys {
		out[k] = n
	}
	for k, n := range c.sites {
		out[k] = max(out[k], n)
	}
	return out
}
