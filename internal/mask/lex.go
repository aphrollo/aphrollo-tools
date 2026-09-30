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
}

// NewLexer compiles a row. It is cheap, and holds no state between calls to
// Lex.
func NewLexer(l lang.Language) *Lexer {
	x := &Lexer{codeEscape: l.CodeEscape}
	x.starts['\\'] = l.CodeEscape
	for _, c := range l.LineComments {
		x.lines = append(x.lines, lineMarker{open: []byte(c.Marker), wordStart: c.WordStart})
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
			placed: s.LineStart || s.OpensAfter != "", after: s.OpensAfter,
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
		depth   int  // open levels of the block comment being read
		escaped bool // the byte before this one was an escaping backslash
		skip    int  // the rest of a delimiter already decided, still to pass
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
		if bytes.HasPrefix(b[i:], m.open) && (!m.wordStart || wordStart(b, i)) {
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
