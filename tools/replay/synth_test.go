package main

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// snapshot is every file of dir and a digest of its bytes.
func snapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSynthesize_SameSeedSameBytes(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := synthesize(a, 7, 6000); err != nil {
		t.Fatal(err)
	}
	if _, err := synthesize(b, 7, 6000); err != nil {
		t.Fatal(err)
	}
	sa, sb := snapshot(t, a), snapshot(t, b)
	if len(sa) < 10 {
		t.Fatalf("only %d files written", len(sa))
	}
	if len(sa) != len(sb) {
		t.Fatalf("two runs of one seed wrote %d and %d files", len(sa), len(sb))
	}
	for name, sum := range sa {
		if sb[name] != sum {
			t.Errorf("%s differs between two runs of one seed", name)
		}
	}
}

func TestSynthesize_AnotherSeedAnotherTree(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := synthesize(a, 1, 6000); err != nil {
		t.Fatal(err)
	}
	if _, err := synthesize(b, 2, 6000); err != nil {
		t.Fatal(err)
	}
	sa, sb := snapshot(t, a), snapshot(t, b)
	same := 0
	for name, sum := range sa {
		if sb[name] == sum {
			same++
		}
	}
	if same == len(sa) {
		t.Fatal("two seeds wrote the same tree: the seed steers nothing")
	}
}

// The file mix is a fanvue-shaped one: TypeScript and Python carry the lines,
// prose and config are a tail.
func TestSynthesize_FileMixIsTypeScriptAndPython(t *testing.T) {
	dir := t.TempDir()
	st, err := synthesize(dir, 3, 40000)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lines < 38000 || st.Lines > 46000 {
		t.Fatalf("wrote %d lines for a budget of 40000", st.Lines)
	}
	byExt := map[string]int{}
	for name := range snapshot(t, dir) {
		data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		byExt[filepath.Ext(name)] += strings.Count(string(data), "\n")
	}
	ts, py := byExt[".ts"]+byExt[".tsx"], byExt[".py"]
	if share := ts * 100 / st.Lines; share < 40 || share > 60 {
		t.Errorf("TypeScript is %d%% of the lines, want 40-60%%: %v", share, byExt)
	}
	if share := py * 100 / st.Lines; share < 25 || share > 45 {
		t.Errorf("Python is %d%% of the lines, want 25-45%%: %v", share, byExt)
	}
	if byExt[".md"] == 0 || byExt[".yml"] == 0 {
		t.Errorf("no prose or config in the mix: %v", byExt)
	}
}

// The suppression comments a consumer's tree carries are in the banks as
// placeholders, so the text of this tool holds none the gate would refuse a
// commit of; the tree it writes has them as comments.
func TestSynthesize_WritesSuppressionCommentsInPlaceOfTheirPlaceholders(t *testing.T) {
	dir := t.TempDir()
	if _, err := synthesize(dir, 5, 40000); err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for name := range snapshot(t, dir) {
		data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		all.Write(data)
	}
	text := all.String()
	for _, want := range []string{"# noqa: E501", "# type: ignore\n", "// @ts-ignore legacy shape", "# pragma: no cover"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tree has no %q", want)
		}
	}
	for _, left := range []string{"__NOQA__", "__NOCOVER__", "__TYPEIGNORE", "__TSIGNORE"} {
		if strings.Contains(text, left) {
			t.Errorf("placeholder %s was left in the tree", left)
		}
	}
}

func TestPickFiles_FixedEvenSpreadOfJudgeableFiles(t *testing.T) {
	var all []string
	for i := range 100 {
		all = append(all, "src/f"+string(rune('a'+i/26))+string(rune('a'+i%26))+".ts")
	}
	all = append(all, "README.md", "data.json", "web/a.py", "x/b_test.go")
	got := pickFiles(all, 10)
	if len(got) != 10 {
		t.Fatalf("picked %d files, want 10: %v", len(got), got)
	}
	if !slices.IsSorted(got) {
		t.Errorf("not in sorted order: %v", got)
	}
	for _, f := range got {
		if f == "README.md" || f == "data.json" {
			t.Errorf("%s is not a file the gate judges the code of", f)
		}
	}
	if again := pickFiles(slices.Clone(all), 10); !slices.Equal(got, again) {
		t.Errorf("a second pick differs: %v vs %v", got, again)
	}
	if few := pickFiles([]string{"a.ts", "b.py"}, 10); !slices.Equal(few, []string{"a.ts", "b.py"}) {
		t.Errorf("fewer files than asked for must all be taken: %v", few)
	}
}
