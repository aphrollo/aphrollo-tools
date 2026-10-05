package ratchet

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/mask"
	"github.com/aphrollo/aphrollo-tools/internal/oracle"
)

// KindOracleSmell: a test-oracle smell or a silenced quality gate, named by
// `detector` (internal/oracle): a real-time sleep, a tautology, a focused or
// disabled test, an error asserted without its kind, a panic-only oracle, a
// lint, type or coverage suppression. The detector reads the file as its own
// language lexes it, strings and comments blanked (strings only for a
// suppression, whose directives live in comments), so a law needs neither
// `code_only` nor `mask_strings`; each hit is the line the smell sits on, and
// the law's own `escape` admits a hit by the usual rule.
const KindOracleSmell MatcherKind = "oracle-smell"

// oracleSmellHits runs the law's detector over the whole file, in the views the
// edit-time smell gate reads: the file's language's lexer, never the law's
// comment prefix, which is one language for a scope that may span several.
func (l Law) oracleSmellHits(file string, fl *FileLines) []Hit {
	lexer := mask.ForFile(fl.langs, file, l.scanView())
	in := oracle.Input{Rows: fl.langs.Rows()}
	if oracle.ReadsDirectives(l.Matcher.Detector) {
		in.Directives = lexer.Lex(fl.text, true, false)
	} else {
		in.Code = lexer.Lex(fl.text, true, true)
	}
	lines, _ := oracle.Detect(l.Matcher.Detector, in)
	var hits []Hit
	for _, n := range lines {
		if n < 1 || n > len(fl.raw) || l.escaped(file, fl.raw, n-1) {
			continue
		}
		hits = append(hits, l.hit(file, n, strings.TrimSpace(fl.raw[n-1])))
	}
	return hits
}
