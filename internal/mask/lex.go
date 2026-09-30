package mask

import (
	"bytes"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// Lexer is the one lexer, reading a language row: the row says where comments
// and strings open and close, and the lexer walks the source once, blanking
// the bytes of the categories it is asked for. Recognition is mandatory and
// blanking selective, so a quote inside a comment opens no string whichever
// categories are blanked.
type Lexer struct {
	codeEscape bool
	lines      []lineMarker
	blocks     []blockMarker
	strs       []strForm
	// starts marks the bytes code can turn on: the first byte of a marker or
	// opener, and a backslash that escapes. Every other byte of code is passed
	// without asking each marker in turn.
	starts [256]bool
}

type lineMarker struct {
	open      []byte
	wordStart bool
	// except are openers that begin with open and are code.
	except [][]byte
}

type blockMarker struct {
	open, close []byte
	nested      bool
}

type strForm struct {
	open, close []byte
	// doubled is close twice: in a doubling string it is one literal closer.
	doubled                       []byte
	escape                        lang.Escape
	multiline, blankOpen, charLit bool
	placed                        bool
	after                         string
	// heredoc: open is an operator followed by an identifier line; the body
	// ends at a line holding that identifier (see heredocHead).
	heredoc bool
}

// NewLexer compiles a row. It is cheap, and holds no state between calls to
// Lex.
func NewLexer(l lang.Language) *Lexer {
	x := &Lexer{codeEscape: l.CodeEscape}
	x.starts['\\'] = l.CodeEscape
	var except [][]byte
	for _, e := range l.LineExcept {
		except = append(except, []byte(e))
	}
	for _, c := range l.LineComments {
		x.lines = append(x.lines, lineMarker{open: []byte(c.Marker), wordStart: c.WordStart, except: except})
		x.starts[c.Marker[0]] = true
	}
	for _, c := range l.BlockComments {
		x.blocks = append(x.blocks, blockMarker{open: []byte(c.Open), close: []byte(c.Close), nested: c.Nested})
		x.starts[c.Open[0]] = true
	}
	for _, s := range l.Strings {
		x.strs = append(x.strs, strForm{
			open: []byte(s.Open), close: []byte(s.Close), doubled: []byte(s.Close + s.Close),
			escape: s.Escape, multiline: s.Multiline, blankOpen: s.BlankOpen, charLit: s.CharLiteral,
			placed: s.LineStart || s.OpensAfter != "", after: s.OpensAfter, heredoc: s.Heredoc,
		})
		x.starts[s.Open[0]] = true
	}
	return x
}

// The lexer's modes.
const (
	inCode = iota
	inLine
	inBlock
	inString
)

// Lex blanks the contents of the row's strings when blankStrings is set and
// of its comments, markers included, when blankComments is set: every blanked
// byte but a newline becomes a space, so length, offsets and lines survive.
// A string's own delimiters stay.
//
// It walks with one range loop and carries its state from byte to byte; no
// index is ever stepped by hand, so no mutation of the scan can walk it
// backwards.
func (x *Lexer) Lex(src string, blankStrings, blankComments bool) string {
	b := []byte(src)
	var (
		mode    = inCode
		form    *strForm
		block   *blockMarker
		depth   int    // open levels of the block comment being read
		escaped bool   // the byte before this one was an escaping backslash
		skip    int    // the rest of a delimiter already decided, still to pass
		term    []byte // the identifier that closes the heredoc being read
	)
	blank := func(cond bool, i int) {
		if cond && b[i] != '\n' {
			b[i] = ' '
		}
	}
	blankRun := func(cond bool, from, n int) {
		for k := range n {
			blank(cond, from+k)
		}
	}
	for i, c := range b {
		switch {
		case skip > 0:
			skip--
			continue
		case escaped:
			escaped = false
			blank(mode == inString && blankStrings, i)
			continue
		}
		switch mode {
		case inLine:
			if c == '\n' {
				mode = inCode
			}
			blank(blankComments, i)
		case inBlock:
			switch {
			case block.nested && bytes.HasPrefix(b[i:], block.open):
				depth++
				blankRun(blankComments, i, len(block.open))
				skip = len(block.open) - 1
			case bytes.HasPrefix(b[i:], block.close):
				depth--
				blankRun(blankComments, i, len(block.close))
				skip = len(block.close) - 1
				if depth == 0 {
					mode = inCode
				}
			default:
				blank(blankComments, i)
			}
		case inString:
			switch {
			case form.heredoc:
				// The closing line starts after this newline, so the check
				// looks one line ahead and passes it in one skip, blanks
				// before the identifier included.
				if c == '\n' {
					if n := heredocCloseLen(b[i+1:], term); n > 0 {
						mode = inCode
						skip = n
					}
				}
				blank(blankStrings, i)
			case form.escape == lang.EscapeDoubling && bytes.HasPrefix(b[i:], form.doubled):
				blankRun(blankStrings, i, len(form.doubled))
				skip = len(form.doubled) - 1
			case bytes.HasPrefix(b[i:], form.close):
				mode = inCode
				skip = len(form.close) - 1
			case c == '\n' && !form.multiline:
				mode = inCode
			case c == '\\' && form.escape == lang.EscapeBackslash:
				escaped = true
				blank(blankStrings, i)
			default:
				blank(blankStrings, i)
			}
		default:
			if !x.starts[c] {
				continue
			}
			if x.codeEscape && c == '\\' {
				escaped = true
				continue
			}
			if m := x.lineAt(b, i); m != nil {
				mode = inLine
				blankRun(blankComments, i, len(m.open))
				skip = len(m.open) - 1
				continue
			}
			if m := x.blockAt(b, i); m != nil {
				mode, block, depth = inBlock, m, 1
				blankRun(blankComments, i, len(m.open))
				skip = len(m.open) - 1
				continue
			}
			f := x.stringAt(b, i)
			if f == nil {
				continue
			}
			if f.charLit {
				// A lifetime's `'` is code: only a literal that closes on
				// this line in a char literal's shape is a string. Advance by
				// the literal's length with a skip count: an index computed as
				// `i + n` is an arithmetic mutation site whose `i - n` walks
				// the scan backwards forever.
				if n, ok := charLiteralLen(b, i); ok {
					blankRun(blankStrings, i+1, n-1)
					skip = n
				}
				continue
			}
			if f.heredoc {
				// `<<<` with no identifier line after it is code.
				if n, id, ok := heredocHead(b, i, len(f.open)); ok {
					mode, form, term = inString, f, id
					skip = n - 1
				}
				continue
			}
			mode, form = inString, f
			blankRun(blankStrings && f.blankOpen, i, len(f.open))
			skip = len(f.open) - 1
		}
	}
	return string(b)
}

// lineAt is the line comment marker that opens at b[i], or nil.
func (x *Lexer) lineAt(b []byte, i int) *lineMarker {
	for k := range x.lines {
		m := &x.lines[k]
		if bytes.HasPrefix(b[i:], m.open) && (!m.wordStart || wordStart(b, i)) && !startsAny(b[i:], m.except) {
			return m
		}
	}
	return nil
}

// blockAt is the block comment opener at b[i], or nil.
func (x *Lexer) blockAt(b []byte, i int) *blockMarker {
	for k := range x.blocks {
		if bytes.HasPrefix(b[i:], x.blocks[k].open) {
			return &x.blocks[k]
		}
	}
	return nil
}

// stringAt is the string form that opens at b[i], or nil. The forms are
// ordered longest opener first, so a triple quote is taken before the single
// quote it begins with.
func (x *Lexer) stringAt(b []byte, i int) *strForm {
	for k := range x.strs {
		f := &x.strs[k]
		if bytes.HasPrefix(b[i:], f.open) && (!f.placed || scalarStart(b, i, f.after)) {
			return f
		}
	}
	return nil
}

// startsAny reports whether b begins with one of prefixes.
func startsAny(b []byte, prefixes [][]byte) bool {
	for _, p := range prefixes {
		if bytes.HasPrefix(b, p) {
			return true
		}
	}
	return false
}

// heredocHead reads the head of a heredoc whose operator of length openLen
// stands at b[i]: blanks, an optionally quoted identifier that does not start
// with a digit, and a line end. n is the head's length in bytes up to the line
// end, id the identifier; ok is false where the operator is no heredoc's.
func heredocHead(b []byte, i, openLen int) (n int, id []byte, ok bool) {
	rest := bytes.TrimLeft(b[i+openLen:], " \t")
	var quote byte
	if len(rest) > 0 && (rest[0] == '\'' || rest[0] == '"') {
		quote, rest = rest[0], rest[1:]
	}
	idLen := identLen(rest)
	if idLen == 0 || rest[0] >= '0' && rest[0] <= '9' {
		return 0, nil, false
	}
	id, rest = rest[:idLen], rest[idLen:]
	if quote != 0 {
		if len(rest) == 0 || rest[0] != quote {
			return 0, nil, false
		}
		rest = rest[1:]
	}
	rest = bytes.TrimPrefix(rest, []byte{'\r'})
	if len(rest) == 0 || rest[0] != '\n' {
		return 0, nil, false
	}
	return len(b[i:]) - len(rest), id, true
}

// heredocCloseLen is the length of the closing line's start that opens line:
// the blanks before the identifier id and the identifier itself, where no byte
// that could continue the identifier follows. It is 0 when line does not close
// the heredoc.
func heredocCloseLen(line, id []byte) int {
	trimmed := bytes.TrimLeft(line, " \t")
	after, found := bytes.CutPrefix(trimmed, id)
	if !found || identLen(after) > 0 {
		return 0
	}
	return len(line) - len(after)
}

// identLen is the length of the identifier at the start of b: letters, digits
// and underscores.
func identLen(b []byte) int {
	for k, c := range b {
		if c != '_' && (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return k
		}
	}
	return len(b)
}

// wordStart reports whether b[i] begins a shell word: it opens the input or
// follows a blank, a newline or an operator character.
func wordStart(b []byte, i int) bool {
	return i == 0 || bytes.IndexByte([]byte(" \t\n;&|()<>"), b[i-1]) >= 0
}

// scalarStart reports whether b[i] stands where a scalar can start: the
// nearest byte before it on its line that is not a blank is absent, or is one
// of after.
func scalarStart(b []byte, i int, after string) bool {
	for j := i - 1; j >= 0; j-- {
		switch b[j] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		}
		return bytes.IndexByte([]byte(after), b[j]) >= 0
	}
	return true
}
