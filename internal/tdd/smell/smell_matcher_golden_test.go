package smell

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The smells are matcher kinds of the law engine as well as policies of the
// edit gate, and the two must say the same thing, escapes included: a line the
// gate waves through must not be refused by the law at commit (issue #968). This
// is the golden. For every text of a corpus, the policy (the old detector, as the
// gate runs it) and the preset law (the new matcher, as `ratchet check` runs it)
// name the same lines. A line-local smell is held to the exact set: the lines the
// gate hits judging that one line as added, and the lines the law names. A smell
// that reads its neighbours (an error-kind safety net two lines either side, a
// test's whole body) is held to the whole file, where both say the same thing,
// and to each line the law names being a hit of the gate over its neighbourhood.
// The corpus is each policy's own fixtures (hit, clean and escaped) read as
// every language the smells know, the law fixtures of this repo, and every Go
// file of the module.

// smellgoldenLaw is the preset law of a policy, as shipped.
func smellgoldenLaw(t *testing.T, policyName string) ratchet.Law {
	t.Helper()
	name := strings.ReplaceAll(policyName, "-", "_")
	raw, err := ratchet.LoadPresetText("smells", name)
	if err != nil {
		t.Fatalf("policy %s has no preset law: %v", policyName, err)
	}
	text, missing := ratchet.RenderPresetText(raw, nil)
	if len(missing) != 0 {
		t.Fatalf("preset %s needs params %v", name, missing)
	}
	law, err := ratchet.ParseLaw(text, name)
	if err != nil {
		t.Fatalf("preset %s: %v", name, err)
	}
	return law
}

type smellgoldenText struct{ path, content string }

func smellgoldenRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test")
		}
		dir = parent
	}
}

func smellgoldenCorpus(t *testing.T) []smellgoldenText {
	t.Helper()
	root := smellgoldenRoot(t)
	var out []smellgoldenText
	exts := []string{".go", ".py", ".ts", ".rs", ".zig"}
	fixtures, _ := filepath.Glob(filepath.Join("testdata", "policies", "*", "*.txt"))
	for _, f := range fixtures {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, ext := range exts {
			out = append(out, smellgoldenText{"fixture" + ext, string(data)})
		}
	}
	for _, base := range []string{".ratchet/fixtures", "internal", "cmd"} {
		_ = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(base)), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !slices.Contains([]string{".go", ".py", ".ts", ".rs", ".zig"}, filepath.Ext(p)) {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			out = append(out, smellgoldenText{filepath.ToSlash(p), string(data)})
			return nil
		})
	}
	if len(out) < 500 {
		t.Fatalf("corpus holds %d texts, too few to prove anything", len(out))
	}
	return out
}

// smellgoldenContextual are the policies whose verdict on a line depends on the
// lines around it.
var smellgoldenContextual = map[string]bool{"error-kind-blind": true, "panic-only-oracle": true}

// smellgoldenFires reports whether the gate, judging exactly the lines, refuses
// or warns of the policy: the verdict of an edit that adds those lines, escapes
// honoured.
func smellgoldenFires(p policy, text smellgoldenText, lines map[int]bool) bool {
	d := evaluateAdded(text.content, lines, langOf("", text.path), []policy{p}, commitPhase)
	return d.Policy == p.name
}

func smellgoldenLineSet(from, to, last int) map[int]bool {
	out := map[int]bool{}
	for n := max(from, 1); n <= min(to, last); n++ {
		out[n] = true
	}
	return out
}

func TestSmellMatchers_AgreeWithTheEditGatePoliciesOverTheCorpus(t *testing.T) {
	policies := testPolicies
	laws := map[string]ratchet.Law{}
	for _, p := range policies {
		laws[p.name] = smellgoldenLaw(t, p.name)
	}
	hits := map[string]int{}
	for _, text := range smellgoldenCorpus(t) {
		total := strings.Count(text.content, "\n") + 1
		everything := smellgoldenLineSet(1, total, total)
		for _, p := range policies {
			found := laws[p.name].HitsIn(text.path, text.content)
			named := map[int]bool{}
			for _, h := range found {
				named[h.Line] = true
			}
			whole := smellgoldenFires(p, text, everything)
			if whole != (len(named) > 0) {
				t.Errorf("%s over %s: the gate says %v over the whole file, the law names %d line(s)", p.name, text.path, whole, len(named))
				continue
			}
			if !whole {
				continue
			}
			hits[p.name]++
			if smellgoldenContextual[p.name] {
				for n := range named {
					window := smellgoldenLineSet(n-2, n+2, total)
					if p.name == "panic-only-oracle" {
						window = smellgoldenLineSet(n, total, total)
					}
					if !smellgoldenFires(p, text, window) {
						t.Errorf("%s over %s: the law names line %d and the gate does not fire over its neighbourhood", p.name, text.path, n)
					}
				}
				continue
			}
			gate := map[int]bool{}
			for n := 1; n <= total; n++ {
				if smellgoldenFires(p, text, map[int]bool{n: true}) {
					gate[n] = true
				}
			}
			if !maps.Equal(gate, named) {
				t.Errorf("%s over %s: the gate hits lines %v, the law names %v", p.name, text.path, slices.Sorted(maps.Keys(gate)), slices.Sorted(maps.Keys(named)))
			}
		}
	}
	for _, p := range policies {
		if hits[p.name] == 0 {
			t.Errorf("%s: no text of the corpus trips it, so the agreement proves only silence", p.name)
		}
	}
}

// The escape fixtures pin the equivalence where it was broken: a bare token and
// a token with a reason admit the line they sit on and the one below, for the
// gate and for the law alike, and the lines they do not reach are still named.
func TestSmellMatchers_AnEscapedLineIsAdmittedByTheGateAndTheLawAlike(t *testing.T) {
	want := map[string][]int{
		"test-sleep":        {4, 6},
		"disabled-test":     {4, 6},
		"error-kind-blind":  {7},
		"panic-only-oracle": {13},
	}
	for name, lines := range want {
		data, err := os.ReadFile(filepath.Join("testdata", "policies", name, "escaped.txt"))
		if err != nil {
			t.Fatal(err)
		}
		var p policy
		for _, q := range testPolicies {
			if q.name == name {
				p = q
			}
		}
		var got []int
		for _, h := range smellgoldenLaw(t, name).HitsIn("fixture.go", string(data)) {
			got = append(got, h.Line)
		}
		if !slices.Equal(got, lines) {
			t.Errorf("%s: the law names lines %v, want %v", name, got, lines)
		}
		total := strings.Count(string(data), "\n") + 1
		for n := 1; n <= total; n++ {
			if smellgoldenContextual[name] {
				continue
			}
			if fired := smellgoldenFires(p, smellgoldenText{"fixture.go", string(data)}, map[int]bool{n: true}); fired != slices.Contains(lines, n) {
				t.Errorf("%s: the gate fires on line %d = %v, want %v", name, n, fired, slices.Contains(lines, n))
			}
		}
		if got := smellgoldenFires(p, smellgoldenText{"fixture.go", string(data)}, smellgoldenLineSet(1, total, total)); !got {
			t.Errorf("%s: the gate does not fire over the whole fixture, whose unescaped lines it must still name", name)
		}
	}
}
