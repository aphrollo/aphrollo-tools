package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A law nobody proved catches nothing. The consuming repo's hand-written
// guards had each grown a "the scanner actually sees the shape it is looking
// for" test for exactly one reason: a scanner that silently stopped matching
// reports a clean tree forever, and the green reads as coverage. Here that
// test is data too — `hit/` files whose offences are listed in `expected.txt`,
// and `clean/` files that must produce none. Both directions are required:
// hit-only proves the rule fires but not that it discriminates.
const FixturesDir = ".ratchet/fixtures"

// FixtureResult is one law's fixture verdict.
type FixtureResult struct {
	Law        string   `json:"law"`
	HitFiles   int      `json:"hit_files"`
	CleanFiles int      `json:"clean_files"`
	Failures   []string `json:"failures,omitempty"`
}

// RunFixtures runs every law against its own fixtures.
func RunFixtures(root string) ([]FixtureResult, error) {
	laws, err := LoadLaws(root)
	if err != nil {
		return nil, err
	}
	var out []FixtureResult
	for _, law := range laws {
		out = append(out, runLawFixtures(root, law))
	}
	return out, nil
}

func runLawFixtures(root string, law Law) FixtureResult {
	res := FixtureResult{Law: law.Name}
	dir := filepath.Join(root, filepath.FromSlash(FixturesDir), law.Name)
	rel := FixturesDir + "/" + law.Name
	if _, err := os.Stat(dir); err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf(
			"no fixtures at %s — a law nobody proved catches nothing", rel))
		return res
	}

	hit, err := fixtureHits(dir, "hit", law)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	clean, err := fixtureHits(dir, "clean", law)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	res.HitFiles, res.CleanFiles = hit.files, clean.files
	if hit.files == 0 {
		res.Failures = append(res.Failures, fmt.Sprintf("%s/hit holds no fixture file", rel))
	}
	if clean.files == 0 {
		res.Failures = append(res.Failures, fmt.Sprintf(
			"%s/clean holds no fixture file — a law with no clean case proves only that it fires, never that it discriminates", rel))
	}

	expected, err := readExpected(filepath.Join(dir, "expected.txt"))
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}
	got := map[string]bool{}
	for _, h := range hit.hits {
		got[expectationID(h)] = true
	}
	for _, want := range sortedKeys(expected) {
		if !got[want] {
			res.Failures = append(res.Failures, fmt.Sprintf("expected a hit at %s/hit/%s — none was produced", rel, want))
		}
	}
	for _, have := range sortedKeys(got) {
		if !expected[have] {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"unexpected hit at %s/hit/%s — list it in expected.txt or fix the law", rel, have))
		}
	}
	for _, h := range clean.hits {
		res.Failures = append(res.Failures, fmt.Sprintf(
			"clean fixture %s/clean/%s:%d hit: %s", rel, h.File, h.Line, h.What))
	}
	return res
}

type fixtureScan struct {
	files int
	hits  []Hit
}

// fixtureHits applies one law to every file under <dir>/<sub>, treating that
// directory as the whole world — the law's scope globs are written against the
// real tree, and a fixture lives somewhere else entirely.
func fixtureHits(dir, sub string, law Law) (fixtureScan, error) {
	base := filepath.Join(dir, sub)
	var scan fixtureScan
	content := map[string]string{}
	var files []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		relPath, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		rel := filepath.ToSlash(relPath)
		files = append(files, rel)
		content[rel] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return scan, fmt.Errorf("reading fixtures in %s: %w", base, err)
	}
	sort.Strings(files)
	scan.files = len(files)

	law.Root = base
	if wholeTreeKind(law.Matcher.Kind) {
		if scan.files == 0 {
			return scan, nil
		}
		hits, err := fixtureWholeTreeHits(base, law, files, content)
		if err != nil {
			return scan, err
		}
		scan.hits = hits
		return scan, nil
	}
	for _, rel := range files {
		scan.hits = append(scan.hits, law.HitsIn(rel, content[rel])...)
	}
	return scan, nil
}

// expectationID names one hit in expected.txt: `<file>:<line>` for a law that
// points at a line, and the hit's own key for a whole-tree law, which has no
// line to point at and whose key is already the readable identity.
func expectationID(h Hit) string {
	if h.Line > 0 {
		return fmt.Sprintf("%s:%d", h.File, h.Line)
	}
	return h.Key
}

// readExpected parses one expectation per line (see expectationID); `#` and
// blank lines are comments.
func readExpected(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no expected.txt beside the fixtures (%s) — a hit fixture with no expectation asserts nothing", path)
	}
	out := map[string]bool{}
	for _, line := range splitLines(string(data)) {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		out[text] = true
	}
	return out, nil
}

// fixtureWholeTreeHits answers a whole-tree law with the fixture directory as
// the whole world: its registry file, its checked-in `cargo metadata`
// document, its own pair of files, its own generated JSON.
func fixtureWholeTreeHits(base string, law Law, files []string, content map[string]string) ([]Hit, error) {
	switch law.Matcher.Kind {
	case KindRegistryBothWays:
		return registryHits(base, law, files, content, false, true)
	case KindDepGraphForbids:
		return depGraphHits(base, law)
	case KindFileSetContainment:
		return containmentHits(base, law)
	case KindJSONNumberCeiling:
		return jsonCeilingHits(base, law, false)
	}
	return nil, nil
}
