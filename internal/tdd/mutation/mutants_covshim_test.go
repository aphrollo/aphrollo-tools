package mutation

import (
	"context"
	"io"
)

// buildTestMap measures the package of dir the way a commit that changes every
// one of its functions would: the fixture's mutants sit at lines 4 and 8 of
// p.go, so every test that can reach either is measured. built is false for a
// package with no tests.
func buildTestMap(ctx context.Context, root string, cfg MutantsConfig, dir string, workers int, log io.Writer) (testMap, bool, error) {
	mutants := []commitMutant{{File: dir + "/p.go", Line: 4}, {File: dir + "/p.go", Line: 8}}
	res, err := ensureCoverage(ctx, root, cfg, covRequest{Dir: dir, Mutants: mutants, Workers: workers}, log)
	if err != nil || !res.Built {
		return testMap{}, false, err
	}
	return res.Map, true, nil
}
