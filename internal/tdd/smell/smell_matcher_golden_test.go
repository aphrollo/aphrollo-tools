package smell

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The smells are matcher kinds of the law engine as well as policies of the
// edit gate, and the two must say the same thing. This is the golden: for every
// text of a corpus, the policy (the old detector, as the gate runs it) and the
// preset law (the new matcher, as `ratchet check` runs it) agree on whether the
// smell is there, and the lines the law names are lines the policy hits on.
// The corpus is each policy's own fixtures read as every language the smells
// know, the law fixtures of this repo, and every Go file of the module.

// smellgoldenLaw is the preset law of a policy, with the law engine's own
// escape (which asks for a reason) taken off: the gate's escape is judged on
// the lines an edit adds, a different question from detection.
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
	law.Escape = ""
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

func TestSmellMatchers_AgreeWithTheEditGatePoliciesOverTheCorpus(t *testing.T) {
	policies := testPolicies
	laws := map[string]ratchet.Law{}
	for _, p := range policies {
		laws[p.name] = smellgoldenLaw(t, p.name)
	}
	hits := map[string]int{}
	for _, text := range smellgoldenCorpus(t) {
		v := newView(text.content, langOf("", text.path))
		for _, p := range policies {
			law := laws[p.name]
			found := law.HitsIn(text.path, text.content)
			if old := p.hit(v); old != (len(found) > 0) {
				t.Errorf("%s over %s: the policy says %v, the law names %d line(s)", p.name, text.path, old, len(found))
				continue
			}
			if len(found) == 0 {
				continue
			}
			hits[p.name]++
			if p.name == "panic-only-oracle" {
				continue // its hit is the declaration line; the body is what the policy reads
			}
			lines := map[int]bool{}
			for _, h := range found {
				lines[h.Line] = true
			}
			if !p.hit(restrict(v, lines)) {
				t.Errorf("%s over %s: the policy does not hit on the line(s) %v the law names", p.name, text.path, lines)
			}
		}
	}
	for _, p := range policies {
		if hits[p.name] == 0 {
			t.Errorf("%s: no text of the corpus trips it, so the agreement proves only silence", p.name)
		}
	}
}
