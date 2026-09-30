package mask

import "bytes"

// The lexers the language table replaced, kept here verbatim as the oracle
// the parity tests hold the generic lexer against: a language's output may not
// drift from what these produced, or every baseline written under them reads
// differently (a law's ceiling counts hits on lines it never saw). They are
// test-only; nothing ships them.

// oracleDialect is what separated the `#`-comment languages from one another.
type oracleDialect struct {
	triple, lineStrings, rawSingle, wordHash, quoteDoubling, scalarStart bool
}

var (
	oraclePython = oracleDialect{triple: true, lineStrings: true}
	oracleShell  = oracleDialect{rawSingle: true, wordHash: true}
	oracleTOML   = oracleDialect{triple: true, lineStrings: true, rawSingle: true}
	oracleRuby   = oracleDialect{lineStrings: true}
	oracleYAML   = oracleDialect{lineStrings: true, rawSingle: true, wordHash: true, quoteDoubling: true, scalarStart: true}
)

// oracleTokens is the old neutral lexer, with its Rust char-literal mode.
func oracleTokens(src string, blankStrings, blankComments, hashComment, rustChars bool) string {
	b := []byte(src)
	n := len(b)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; i++ {
		if rustChars && b[i] == '\'' {
			if n, ok := charLiteralLen(b, i); ok {
				for k := 1; k < n; k++ {
					blank(blankStrings, i+k)
				}
				i += n
			}
			continue
		}
		switch b[i] {
		case '\'', '"', '`':
			quote := b[i]
			escapes := quote != '`'
			for i++; i < n && b[i] != quote; i++ {
				if escapes && b[i] == '\\' {
					blank(blankStrings, i)
					i++
					if i < n {
						blank(blankStrings, i)
					}
					continue
				}
				blank(blankStrings, i)
			}
		case '/':
			if i+1 < n && b[i+1] == '/' {
				blank(blankComments, i)
				for i++; i < n && b[i] != '\n'; i++ {
					blank(blankComments, i)
				}
			} else if i+1 < n && b[i+1] == '*' {
				blank(blankComments, i)
				blank(blankComments, i+1)
				for i += 2; i < n; i++ {
					if b[i] == '*' && i+1 < n && b[i+1] == '/' {
						blank(blankComments, i)
						blank(blankComments, i+1)
						i++
						break
					}
					blank(blankComments, i)
				}
			}
		case '#':
			if !hashComment {
				continue
			}
			for ; i < n && b[i] != '\n'; i++ {
				blank(blankComments, i)
			}
		case '\\':
			if i+1 < n && b[i+1] == '\\' && oracleLeadingWhitespace(b, i) {
				for ; i < n && b[i] != '\n'; i++ {
					blank(blankStrings, i)
				}
			}
		}
	}
	return string(b)
}

// oracleHashTokens is the old `#`-comment lexer.
func oracleHashTokens(src string, blankStrings, blankComments bool, d oracleDialect) string {
	b := []byte(src)
	var (
		in      byte
		triple  bool
		escaped bool
		skip    int
	)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i, c := range b {
		switch {
		case skip > 0:
			skip--
		case escaped:
			escaped = false
			blank(in != 0 && blankStrings, i)
		case in == '#':
			if c == '\n' {
				in = 0
			}
			blank(blankComments, i)
		case in != 0:
			switch {
			case d.quoteDoubling && c == '\'' && in == '\'' && i+1 < len(b) && b[i+1] == '\'':
				escaped = true
				blank(blankStrings, i)
			case c == in && (!triple || bytes.HasPrefix(b[i:], []byte{c, c, c})):
				in = 0
				if triple {
					skip = 2
				}
			case c == '\n' && !triple && d.lineStrings:
				in = 0
			case c == '\\' && (in == '"' || !d.rawSingle):
				escaped = true
				blank(blankStrings, i)
			default:
				blank(blankStrings, i)
			}
		case c == '\\':
			escaped = true
		case c == '#' && (!d.wordHash || oracleWordStart(b, i)):
			in = '#'
			blank(blankComments, i)
		case (c == '\'' || c == '"') && (!d.scalarStart || oracleScalarStart(b, i)):
			in = c
			triple = d.triple && bytes.HasPrefix(b[i:], []byte{c, c, c})
			if triple {
				skip = 2
			}
		}
	}
	return string(b)
}

func oracleWordStart(b []byte, i int) bool {
	return i == 0 || bytes.IndexByte([]byte(" \t\n;&|()<>"), b[i-1]) >= 0
}

func oracleScalarStart(b []byte, i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch b[j] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		}
		return bytes.IndexByte([]byte(":-[{,?"), b[j]) >= 0
	}
	return true
}

func oracleLeadingWhitespace(b []byte, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if b[j] == '\n' {
			return true
		}
		if b[j] != ' ' && b[j] != '\t' {
			return false
		}
	}
	return true
}

// oracles maps each language row the table replaced a lexer for to that old
// lexer, in the shape the generic lexer is called.
var oracles = map[string]func(src string, blankStrings, blankComments bool) string{
	"default": func(s string, bs, bc bool) string { return oracleTokens(s, bs, bc, false, false) },
	// The neutral lexer with `#` read as a comment, as the edit-time smells
	// and StringsAndComments read it.
	"default-hash": func(s string, bs, bc bool) string { return oracleTokens(s, bs, bc, true, false) },
	"rust":         func(s string, bs, bc bool) string { return oracleTokens(s, bs, bc, false, true) },
	"python":       func(s string, bs, bc bool) string { return oracleHashTokens(s, bs, bc, oraclePython) },
	"shell":        func(s string, bs, bc bool) string { return oracleHashTokens(s, bs, bc, oracleShell) },
	"toml":         func(s string, bs, bc bool) string { return oracleHashTokens(s, bs, bc, oracleTOML) },
	"ruby":         func(s string, bs, bc bool) string { return oracleHashTokens(s, bs, bc, oracleRuby) },
	"yaml":         func(s string, bs, bc bool) string { return oracleHashTokens(s, bs, bc, oracleYAML) },
}
