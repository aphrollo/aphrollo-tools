package tdd

import (
	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// At COMMIT time a law is judged against its baseline: the commit is what the
// baseline exists to ceiling, and a file already over it must not be allowed
// to stay there. At EDIT time that same comparison refuses edits nobody can
// satisfy. A file carrying two hits the baseline does not cover refused every
// Edit to it -- including the edit REMOVING one of them, which was denied for
// the hit it left behind. A refusal with no move left pushes a builder into
// `sed` or a Python write, and a write the gate never sees is the one outcome
// it must never cause.
//
// So the pre-edit judge compares the file with ITSELF as it stands on disk:
// deny when a law's hit count RISES, and name only the lines the edit added.
// It stays a SUBSET of the commit gate -- a rise the baseline still covers is
// no finding here either, so nothing is denied at edit time that the commit
// would allow.

// editRegressions narrows a pre-edit verdict to the hits this edit ADDS.
// before and after are the file's content on disk and as the edit would leave
// it; res is the law engine's verdict on `after`. A law whose matcher cannot
// answer for a single file -- a registry, a dependency graph, a whole-tree
// count -- keeps its own verdict untouched: there is no per-file count to
// compare, and dropping it would silently retire the rule at edit time.
func editRegressions(root, rel, before, after string, res ratchet.Result) []ratchet.Finding {
	if len(res.Findings) == 0 {
		return nil
	}
	laws, err := ratchet.LoadLaws(root)
	if err != nil {
		return res.Findings // a rule this binary cannot read is not one to soften
	}
	type counted struct {
		before, after int
		added         []ratchet.Hit
	}
	byLaw := map[string]counted{}
	for _, law := range laws {
		if !law.Scope.Matches(rel) {
			continue
		}
		b := law.HitsIn(rel, before)
		a := law.HitsIn(rel, after)
		byLaw[law.Name] = counted{before: len(b), after: len(a), added: addedHits(b, a)}
	}

	var out []ratchet.Finding
	reported := map[string]bool{}
	for _, f := range res.Findings {
		c, known := byLaw[f.Law]
		if !known || (c.before == 0 && c.after == 0) {
			out = append(out, f) // HitsIn cannot answer this law
			continue
		}
		if c.after <= c.before || reported[f.Law] {
			continue
		}
		reported[f.Law] = true
		for _, h := range c.added {
			n := f
			n.File, n.Line, n.What, n.Key = h.File, h.Line, h.What, h.Key
			// The numbers a session can act on are the file's own: what it
			// carried before this edit, and what the edit would leave.
			n.Baseline, n.Measured = c.before, c.after
			out = append(out, n)
		}
	}
	return out
}

// addedHits is the multiset difference after - before, by Key: a key present
// in both is consumed once per prior occurrence, so removing one of two
// identical offences adds nothing and appending a third names the third line.
func addedHits(before, after []ratchet.Hit) []ratchet.Hit {
	budget := map[string]int{}
	for _, h := range before {
		budget[h.Key]++
	}
	var out []ratchet.Hit
	for _, h := range after {
		if budget[h.Key] > 0 {
			budget[h.Key]--
			continue
		}
		out = append(out, h)
	}
	return out
}
