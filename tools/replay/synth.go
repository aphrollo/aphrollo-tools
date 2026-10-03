package main

import (
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The synthetic tree stands in for a large consumer, a TypeScript web app built
// with vite beside Python services. It is written from a seed, so every run on
// every host makes the same bytes, and it holds nothing real: files are
// assembled from snippet banks (synth/*.snip) that hold the constructs lexers
// get wrong (a comment opener inside a string, a quote inside a comment,
// template literals, triple-quoted strings) beside the plain code around them.
// A bank is plain text of the language, so a new case is new text.

//go:embed synth/*.snip
var snipFS embed.FS

// snipSeparator splits a bank into snippets.
const snipSeparator = "\n%%%\n"

// suppressions are the comments a consumer's tree carries to silence a linter or
// a type checker, some with a reason and some without, which the suppression
// laws read. A bank holds them as placeholders: the text of this tool then holds
// none the gate would refuse a commit of.
var suppressions = strings.NewReplacer(
	"__NOQA__", "# noqa: E501",
	"__TYPEIGNORE__", "# type: ignore",
	"__TYPEIGNORE_REASONED__", "# type: ignore[assignment] -- reasoned",
	"__NOCOVER__", "# pragma: no cover",
	"__TSIGNORE__", "// @ts-ignore",
	"__TSIGNORE_REASONED__", "// @ts-ignore: reasoned suppression, the shape the law admits",
)

type synthStats struct{ Files, Lines int }

// synthPlan is one extension's share of the tree.
type synthPlan struct {
	Ext    string
	Dir    string
	Bank   string
	Share  int // percent of the line budget
	Areas  []string
	Header string
}

var synthPlans = []synthPlan{
	{Ext: ".ts", Dir: "web/src/lib", Bank: "ts", Share: 35, Areas: []string{"api", "cache", "parse", "store", "util"},
		Header: "// Synthetic module (generated, not real code).\nimport { helper } from \"./util\";\n\n"},
	{Ext: ".tsx", Dir: "web/src/components", Bank: "ts", Share: 15, Areas: []string{"feed", "inbox", "profile", "search"},
		Header: "// Synthetic component (generated, not real code).\nimport { helper } from \"../lib/util\";\n\n"},
	{Ext: ".py", Dir: "services", Bank: "py", Share: 35, Areas: []string{"billing", "ingest", "notify", "reports", "sync"},
		Header: "\"\"\"Synthetic module (generated, not real code).\"\"\"\nimport os\nimport re\nfrom dataclasses import dataclass\n\n\n"},
	{Ext: ".md", Dir: "docs", Bank: "md", Share: 10, Areas: []string{"guides", "notes", "ops"}},
	{Ext: ".yml", Dir: "deploy", Bank: "yaml", Share: 5, Areas: []string{"base", "prod", "stage"},
		Header: "# Synthetic config (generated, not real).\n"},
}

var synthWords = []string{
	"alpha", "anchor", "basket", "beacon", "bridge", "cabin", "cache", "candle", "carbon", "cedar", "cobalt", "copper",
	"delta", "ember", "falcon", "fjord", "garnet", "harbor", "indigo", "island", "jasper", "kernel", "lantern", "marble",
	"meadow", "nectar", "orbit", "pebble", "quartz", "ribbon", "saddle", "tundra", "umber", "velvet", "willow", "zephyr",
}

// rng is splitmix64: a few lines, fixed forever, the same on every host and Go
// version, which math/rand does not promise for a seeded source.
type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

type synther struct {
	r       *rng
	banks   map[string][]string
	counter int
	// codePaths are the generated source files a Markdown snippet can cite.
	codePaths []string
}

func loadBank(name string) ([]string, error) {
	data, err := snipFS.ReadFile("synth/" + name + ".snip")
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	parts := strings.Split(text, snipSeparator)
	for i := range parts {
		parts[i] += "\n"
	}
	return parts, nil
}

// synthesize writes a tree of about lines lines into dir.
func synthesize(dir string, seed uint64, lines int) (synthStats, error) {
	s := &synther{r: &rng{s: seed}, banks: map[string][]string{}}
	var stats synthStats
	for _, plan := range synthPlans {
		bank, err := loadBank(plan.Bank)
		if err != nil {
			return stats, err
		}
		s.banks[plan.Bank] = bank
		budget, written := lines*plan.Share/100, 0
		for fileNo := 0; written < budget; fileNo++ {
			area := plan.Areas[s.r.intn(len(plan.Areas))]
			rel := path.Join(plan.Dir, area, synthWords[s.r.intn(len(synthWords))]+strconv.Itoa(fileNo)+plan.Ext)
			content := s.compose(plan, rel, min(fileTarget(s.r, fileNo), max(budget-written, 12)))
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return stats, err
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				return stats, err
			}
			n := strings.Count(content, "\n")
			written += n
			stats.Files++
			stats.Lines += n
			if plan.Ext != ".md" {
				s.codePaths = append(s.codePaths, rel)
			}
		}
	}
	return stats, nil
}

// fileTarget is how many lines file number fileNo aims for: mostly a few
// hundred, and now and then one past a line-count law's ceiling, because a
// consumer's tree has those too.
func fileTarget(r *rng, fileNo int) int {
	if fileNo == 3 || fileNo%40 == 39 {
		return 700 + r.intn(1800)
	}
	return 40 + r.intn(260)
}

func (s *synther) compose(plan synthPlan, rel string, target int) string {
	var b strings.Builder
	b.WriteString(plan.Header)
	for n := strings.Count(plan.Header, "\n"); n < target; {
		before := b.Len()
		s.writeSnippet(&b, plan.Bank, rel)
		n += strings.Count(b.String()[before:], "\n")
	}
	return b.String()
}

func (s *synther) writeSnippet(b *strings.Builder, bank, rel string) {
	snips := s.banks[bank]
	snip := snips[s.r.intn(len(snips))]
	s.counter++
	snip = suppressions.Replace(snip)
	snip = strings.ReplaceAll(snip, "__N__", strconv.Itoa(s.counter))
	snip = strings.ReplaceAll(snip, "__W__", synthWords[s.r.intn(len(synthWords))])
	for range strings.Count(snip, "__EXIST__") {
		snip = strings.Replace(snip, "__EXIST__", s.existingPath(rel), 1)
	}
	for range strings.Count(snip, "__GONE__") {
		snip = strings.Replace(snip, "__GONE__", s.missingPath(), 1)
	}
	b.WriteString(snip)
	b.WriteString("\n")
}

// existingPath is a generated source file, as a path relative to the Markdown
// file at from.
func (s *synther) existingPath(from string) string {
	if len(s.codePaths) == 0 {
		return s.missingPath()
	}
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(s.codePaths[s.r.intn(len(s.codePaths))]))
	if err != nil {
		return s.missingPath()
	}
	return filepath.ToSlash(rel)
}

// missingPath is a path no generated file has: a directory the generator never
// writes, under a name nobody repeats.
func (s *synther) missingPath() string {
	exts := []string{".ts", ".py", ".tsx"}
	return fmt.Sprintf("gone/%s%d%s", synthWords[s.r.intn(len(synthWords))], s.counter, exts[s.r.intn(len(exts))])
}

// judgeable are the extensions of the files the gate leg judges as edits.
var judgeable = []string{".ts", ".tsx", ".py", ".go", ".rs"}

// pickFiles is the fixed set of n files the gate leg judges: the judgeable ones
// of files in sorted order, spread evenly over the list, so the same tree always
// gives the same set and the set covers every directory the list does.
func pickFiles(files []string, n int) []string {
	var eligible []string
	for _, f := range files {
		if slices.Contains(judgeable, path.Ext(f)) {
			eligible = append(eligible, f)
		}
	}
	slices.Sort(eligible)
	if len(eligible) <= n {
		return eligible
	}
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, eligible[i*len(eligible)/n])
	}
	return out
}
