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
	if law.Baseline == "" {
		return AdoptResult{}, fmt.Errorf("%s declares no baseline — there is nothing to adopt", law.Name)
	}
	path := filepath.Join(opts.Root, filepath.FromSlash(law.Baseline))
	_, statErr := os.Stat(path)
	hasBaseline := statErr == nil
	if hasBaseline && !opts.LawChangedSinceHEAD {
		return AdoptResult{}, fmt.Errorf(
			"%s: a baseline already exists and the law is unchanged since HEAD — adoption is for a new or widened law, not an unrelated raise", law.Name)
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
	if _, err := baseline.WriteIfChanged(path); err != nil {
		return AdoptResult{}, err
	}
	return AdoptResult{Law: law.Name, Path: path, Rows: len(measured)}, nil
}

// adoptHits measures one law over the whole tracked... here, the whole real
// tree — adoption always reads disk, never a proposed or narrowed set, since
// its entire point is to record what is actually there.
func adoptHits(root string, law Law) ([]Hit, error) {
	scan, err := scanTree(Options{Root: root}, []Law{law})
	if err != nil {
		return nil, err
	}
	hits := scan.byLaw[law.Name]
	switch law.Matcher.Kind {
	case KindRegistryBothWays:
		return registryHits(root, law, scan.files, scan.content, true, true)
	case KindDepGraphForbids:
		return depGraphHits(root, law)
	case KindFileSetContainment:
		return containmentHits(root, law)
	case KindJSONNumberCeiling:
		return jsonCeilingHits(root, law, true, cargoTargetDir())
	}
	return hits, nil
}
