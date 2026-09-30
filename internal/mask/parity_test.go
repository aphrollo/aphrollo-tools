package mask

import (
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The generic lexer replaced six per-language lexers. A baseline written
// under them counts the hits a law found on the lines they left visible, so a
// lexer that reads one byte differently turns untouched code into a
// regression in a repo nobody edited. Every test here holds the generic lexer
// byte-for-byte to the oracle it replaced, over every input the repo can
// produce: hand-written edge cases, a generated stream of the characters the
// lexers care about, and every file of this tree read as every language.

// oracleViews are the views a caller asks for: strings, comments, or both.
var oracleViews = [][2]bool{{true, false}, {false, true}, {true, true}, {false, false}}

func checkParity(t *testing.T, row string, src string, views [][2]bool) bool {
	t.Helper()
	lex := rowLexer(row)
	for _, v := range views {
		got, want := lex(src, v[0], v[1]), oracles[row](src, v[0], v[1])
		if got != want {
			t.Errorf("row %s, strings=%v comments=%v on %q:\n got %q\nwant %q", row, v[0], v[1], src, got, want)
			return false
		}
	}
	return true
}

var edgeCases = []string{
	"",
	"\n",
	"x",
	`'`, `"`, "`", `\`, `\\`, "\\\\\n", `#`, `//`, `/*`, `*/`, `/**/`, `/*/`, `/* */ x`,
	`'''`, `"""`, `''''`, `""""`, `"""a"""b"`, `'''it's'''`,
	"fn a<'a>(x: &'a T) { 'outer: loop { break 'outer; } }\n",
	`c == '\''`, `c == '\u{1F600}'`, "c == 'é'", `'\`, `'a`, `'ab'`, "'\n'",
	"let s = \"a\\\"b\"; // it's\nlet t = 'x';\n",
	"/* it's\n a */ let x = 'y';\n",
	"x = 1  # it's\ny = 'ab'\n",
	"s = 'a\\\nb' # it's\nx = 'k'",
	"d = \"\"\"it's\n# no \"q\"\n\"\"\"\nx = 'k'",
	"echo don\\'t # it's\necho 'a\nb' \"c\\\"d\" $# ${#a} a#b",
	"a: 1 # it's\nb: 'xy' # c\nc: don't\nd: 'it''s'\ne: \"a\\\"b\"\n- 'x'\n[ 'y', \"z\" ]\n",
	"s = \"a #{b}\" # don't\nt = 'it\\'s'\n",
	"[a]\nk = '''raw\\ \n 'q' '''\nj = \"\"\"a\\\"\"\"b\"\"\"\nm = 'x' # c\n",
	"  \\\\zig line 'with' \"quotes\"\nnext = 'q'\n\\\\ also\n",
	"a \\\\ not line start 'x'\n",
	"`raw \\` still raw` \"after\" // tail",
	"\"unterminated\nnext 'line\n",
	"'unterminated\n# it's\nx = 'k'",
	"/* unterminated block 'quote",
	"// line\r\n'x'\r\n",
	"é'é'é\"é\"é#é//é",
	"\x00'\x00'\x00",
}

func TestParity_EdgeCasesReadAsEveryLanguage(t *testing.T) {
	for _, src := range edgeCases {
		for row := range oracles {
			checkParity(t, row, src, oracleViews)
		}
	}
}

// pieces are the byte runs the lexers branch on.
var pieces = []string{
	"'", "\"", "`", `\`, `\\`, "#", "//", "/*", "*/", "\n", "\n", " ", " ", "\t", "a", "b", "é",
	"'''", `"""`, ":", "-", "[", "{", ",", "?", ";", "(", ")", "$", "<", ">", "|", "&", "r", "0", "\r",
	"'a'", `'\n'`, `'\''`, "'a", "{#", "#{", "=", "//", "x: ", "\\\\",
}

func TestParity_GeneratedStreamOfLexicalCharacters(t *testing.T) {
	rng := rand.New(rand.NewPCG(1007, 1023))
	const inputs = 6000
	for n := range inputs {
		var b strings.Builder
		for range rng.IntN(60) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		src := b.String()
		for row := range oracles {
			if !checkParity(t, row, src, oracleViews) {
				t.Fatalf("input %d of %d diverged", n, inputs)
			}
		}
	}
}

// treeRoot is the root of the tree these tests live in.
func treeRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0) // tree-read-ok: the corpus is this repository's own tree
	if !ok {
		t.Fatal("cannot locate the tree")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// treeCorpus reads every regular file of the tree but .git, dependency and
// build directories.
func treeCorpus(t *testing.T) map[string]string {
	t.Helper()
	root := treeRoot(t)
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// crossLanguageLimit bounds the size of a file read as every language, not
// only its own: each file is lexed by all eight oracles and all eight rows.
const crossLanguageLimit = 6 << 10

func TestParity_EveryFileOfTheTreeReadAsEveryLanguage(t *testing.T) {
	files := treeCorpus(t)
	if len(files) < 500 {
		t.Fatalf("the tree corpus holds %d files, want at least 500 — the walk found the wrong root", len(files))
	}
	var bytesRead, lexed int
	for rel, src := range files {
		bytesRead += len(src)
		own := ownRow(t, rel)
		for row := range oracles {
			if row != own && len(src) > crossLanguageLimit {
				continue
			}
			views := oracleViews[:3]
			if row == own {
				views = oracleViews
			}
			lexed++
			if !checkParity(t, row, src, views) {
				t.Fatalf("%s diverged under row %s", rel, row)
			}
		}
	}
	t.Logf("tree corpus: %d files, %d bytes, %d file-by-language comparisons (old lexer against the generic one)", len(files), bytesRead, lexed)
}

// ownRow is the oracle row that read a file before the table: Rust, Python,
// shell, TOML, Ruby and YAML by extension, everything else the neutral lexer.
func ownRow(t *testing.T, rel string) string {
	t.Helper()
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".rs":
		return "rust"
	case ".py":
		return "python"
	case ".sh":
		return "shell"
	case ".toml":
		return "toml"
	case ".rb":
		return "ruby"
	case ".yaml", ".yml":
		return "yaml"
	}
	return "default"
}

func FuzzLexer_AgreesWithTheLexerItReplaced(f *testing.F) {
	for _, src := range edgeCases {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		for row := range oracles {
			checkParity(t, row, src, oracleViews)
		}
	})
}
