package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// AdoptOptions configures one baseline ADOPTION: writing a single law's
// baseline to match the tree exactly, rather than only ever lowering it —
// how a law lands its first baseline, or a deliberately widened law lands
// its next one. This package stays git-agnostic: LawChangedSinceHEAD is a
// fact the caller (which already knows how to run git) computed.
type AdoptOptions struct {
	Root string
	Law  string
	// LawChangedSinceHEAD is true when the law's own .toml differs from HEAD
	// (matcher or scope, in whatever sense the caller judges that). Adoption
	// is refused unless this is true or the law has no baseline file yet —
	// otherwise "adopt" is just a hand-raised ceiling with an extra step.
	LawChangedSinceHEAD bool
	// DryRun measures and reports the rows without writing the baseline.
	DryRun bool
}

// AdoptResult is one adoption's outcome.
type AdoptResult struct {
	Law  string
	Path string
	Rows int
}

// Adopt writes law's baseline from what the tree currently measures. It
// refuses when a baseline file already exists AND the law is unchanged since
// HEAD — the guard against adoption becoming a quiet way to raise a ceiling
// nothing about the law actually justifies.
func Adopt(opts AdoptOptions) (AdoptResult, error) {
	laws, err := LoadLaws(opts.Root)
	if err != nil {
		return AdoptResult{}, err
	}
	var law *Law
	for i := range laws {
		if laws[i].Name == opts.Law {
			law = &laws[i]
			break
		}
	}
	if law == nil {
		return AdoptResult{}, fmt.Errorf("no law named %q under %s", opts.Law, LawsDir)
	}
	if law.UnknownKind != "" {
		return AdoptResult{}, fmt.Errorf("%s: matcher kind %q is unknown to this binary — rebuild aphrollo before adopting it", law.Name, law.UnknownKind)
	}
	if law.Baseline == "" {
		return AdoptResult{}, fmt.Errorf("%s declares no baseline — there is nothing to adopt", law.Name)
	}
	path := filepath.Join(opts.Root, filepath.FromSlash(law.Baseline))
	_, statErr := os.Stat(path)
	hasBaseline := statErr == nil
	if hasBaseline && !opts.LawChangedSinceHEAD {
		// A baseline written before the scan-view stamp may be moved onto the
		// current lexers without a changed law, provided the tree is no
		// higher than it under the lexers it was written by: what the move
		// records is then only what those lexers could not read.
		if !legacyBaseline(opts.Root, nil, *law) {
			return AdoptResult{}, fmt.Errorf(
				"%s: a baseline already exists and the law is unchanged since HEAD — adoption is for a new or widened law, not an unrelated raise", law.Name)
		}
		if err := legacyWithinBaseline(opts.Root, *law); err != nil {
			return AdoptResult{}, err
		}
	}

	hits, err := adoptHits(opts.Root, *law)
	if err != nil {
		return AdoptResult{}, err
	}

	form := baselineForm(*law)
	baseline := &Baseline{form: form}
	if hasBaseline {
		if baseline, err = LoadBaseline(path, form); err != nil {
			return AdoptResult{}, err
		}
	}
	rows := adoptOnto(baseline, *law, hits)
	if opts.DryRun {
		return AdoptResult{Law: law.Name, Path: path, Rows: rows}, nil
	}
	if _, err := baseline.WriteIfChanged(path); err != nil {
		return AdoptResult{}, err
	}
	return AdoptResult{Law: law.Name, Path: path, Rows: rows}, nil
}

// adoptHits measures one law over the whole tracked... here, the whole real
// tree — adoption always reads disk, never a proposed or narrowed set, since
// its entire point is to record what is actually there.
func adoptHits(root string, law Law) ([]Hit, error) {
	return adoptHitsIn(Options{Root: root}, law)
}

// adoptOnto sets baseline to exactly what hits measure for law, stamping it
// when the law is one a lexer change can move, and returns the row count. It is
// the one place a baseline is written from a measurement, whether --adopt or
// the automatic migration asks.
func adoptOnto(baseline *Baseline, law Law, hits []Hit) int {
	measured := map[string]int{}
	sites := map[string][]string{}
	for _, h := range hits {
		id := baseline.Identity(h.Key)
		measured[id] += h.Weight
		sites[id] = append(sites[id], h.Key)
	}
	for _, keys := range sites {
		sort.Strings(keys)
	}
	baseline.AdoptWithSites(measured, sites)
	if view := law.touchedView(); view > 1 {
		baseline.StampAt(view)
	}
	return len(measured)
}

// adoptHitsIn measures law over the view opts describes: the disk for
// adoption, the staged tree for the commit guard's recomputation.
func adoptHitsIn(opts Options, law Law) ([]Hit, error) {
	root := opts.Root
	scan, err := scanTree(opts, []Law{law})
	if err != nil {
		return nil, err
	}
	hits := scan.byLaw[law.Name]
	switch law.Matcher.Kind {
	case KindRegistryBothWays:
		return registryHits(viewOf(opts), law, scan.files, scan.content, true, true)
	case KindDepGraphForbids:
		return depGraphHits(root, law)
	case KindDepGraphCeiling:
		return depGraphCeilingHits(root, law)
	case KindFileSetContainment:
		return containmentHits(viewOf(opts), law)
	case KindJSONNumberCeiling, KindGoBenchCeiling:
		return ceilingHits(viewOf(opts), law, true, cargoTargetDir())
	}
	return hits, nil
}

// legacyWithinBaseline refuses a scan-view migration over a tree that is
// already above its baseline as the lexers it was written under read it: that
// is a raise the law does not justify, whatever the lexers are.
func legacyWithinBaseline(root string, law Law) error {
	law.LegacyView = true
	law.ViewVersion, _ = baselineView(root, nil, law)
	hits, err := adoptHits(root, law)
	if err != nil {
		return err
	}
	baseline, err := LoadBaseline(filepath.Join(root, filepath.FromSlash(law.Baseline)), baselineForm(law))
	if err != nil {
		return err
	}
	measured := map[string]int{}
	for _, h := range hits {
		measured[baseline.Identity(h.Key)] += h.Weight
	}
	if over := baseline.Regressions(measured); len(over) > 0 {
		return fmt.Errorf(
			"%s: the tree is above its baseline (%d key(s), first %q) as the baseline's own lexers read it — fix or escape those hits first; adoption would raise a ceiling nothing justifies",
			law.Name, len(over), over[0].Key)
	}
	return nil
}
