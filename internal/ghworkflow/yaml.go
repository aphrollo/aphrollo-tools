package ghworkflow

import (
	"fmt"
	"strconv"
	"strings"
)

// A small reader for the YAML subset GitHub workflow files use: block maps
// and block sequences, plain and quoted scalars, | and > block scalars, and
// single-line flow sequences and maps of scalars. The module takes no YAML
// dependency, so this reads exactly that and refuses everything else by
// naming the line (anchors, aliases, tags, merge keys, multi-document files,
// multi-line plain or quoted scalars, tabs for indentation, explicit keys).
// A construct it cannot read is an error, never a guess. Scalars stay strings:
// `true` and `1` are text here, and a quoted scalar is marked Quoted.

// Kind is the shape of a Node.
type Kind int

// The shapes a Node takes. A key with no value is KindNull.
const (
	KindNull Kind = iota
	KindScalar
	KindMap
	KindList
)

// Node is one parsed YAML value. Map keys keep their file order.
type Node struct {
	Kind   Kind
	Str    string
	Quoted bool
	Keys   []string
	Vals   []*Node
	Items  []*Node
	Line   int
}

// Get is the value under key in a map, nil when absent or not a map.
func (n *Node) Get(key string) *Node {
	if n == nil || n.Kind != KindMap {
		return nil
	}
	for i, k := range n.Keys {
		if k == key {
			return n.Vals[i]
		}
	}
	return nil
}

// Text is a scalar's text, "" for null and for non-scalars.
func (n *Node) Text() string {
	if n == nil || n.Kind != KindScalar {
		return ""
	}
	return n.Str
}

type srcLine struct {
	no     int
	indent int
	raw    string // the line without its line ending
	text   string // the content after the indent, comment stripped, right-trimmed
}

type parser struct {
	lines []srcLine
	pos   int
}

// ParseYAML reads one workflow document.
func ParseYAML(src string) (*Node, error) {
	p := &parser{}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.TrimPrefix(src, "\ufeff")
	for i, raw := range strings.Split(src, "\n") {
		l := srcLine{no: i + 1, raw: raw}
		trimmed := strings.TrimLeft(raw, " ")
		l.indent = len(raw) - len(trimmed)
		if strings.HasPrefix(trimmed, "\t") && strings.TrimSpace(trimmed) != "" {
			return nil, fmt.Errorf("line %d: a tab in the indentation is not valid YAML", l.no)
		}
		l.text = strings.TrimRight(stripComment(trimmed), " \t")
		p.lines = append(p.lines, l)
	}
	p.skipBlank()
	if p.pos < len(p.lines) && p.lines[p.pos].text == "---" {
		p.pos++
	}
	n, err := p.parseBlock(0)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.text == "---" || l.text == "..." {
			return nil, fmt.Errorf("line %d: a second YAML document is not supported", l.no)
		}
		return nil, fmt.Errorf("line %d: unexpected content %q", l.no, l.text)
	}
	return n, nil
}

// stripComment drops a trailing comment: a # at the start of the text or after
// whitespace, outside quotes.
func stripComment(s string) string {
	var quote byte
	skip := false
	for i := range len(s) {
		if skip {
			skip = false
			continue
		}
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				skip = true
			} else if c == quote && quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				skip = true // '' inside a single-quoted scalar is a quote, not its end
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			if i == 0 || strings.IndexByte(" [{,:-", s[i-1]) >= 0 {
				quote = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

func (p *parser) skipBlank() {
	for p.pos < len(p.lines) && p.lines[p.pos].text == "" {
		p.pos++
	}
}

func isDocMarker(l srcLine) bool {
	return l.indent == 0 && (l.text == "---" || l.text == "...")
}

func isSeqItem(text string) bool { return text == "-" || strings.HasPrefix(text, "- ") }

// parseBlock reads the block node starting at the current line, whose indent
// must be at least min.
func (p *parser) parseBlock(minIndent int) (*Node, error) {
	p.skipBlank()
	if p.pos >= len(p.lines) {
		return &Node{Kind: KindNull}, nil
	}
	l := p.lines[p.pos]
	if l.indent < minIndent {
		return &Node{Kind: KindNull}, nil
	}
	if isSeqItem(l.text) {
		return p.parseSeq(l.indent)
	}
	if _, _, ok, err := splitKey(l); err != nil {
		return nil, err
	} else if ok {
		return p.parseMap(l.indent)
	}
	return nil, fmt.Errorf("line %d: a scalar on a line of its own is not supported: %q", l.no, l.text)
}

func (p *parser) parseMap(indent int) (*Node, error) {
	n := &Node{Kind: KindMap, Line: p.lines[p.pos].no}
	for range p.lines {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			return n, nil
		}
		l := p.lines[p.pos]
		if l.indent < indent || isDocMarker(l) {
			return n, nil
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", l.no)
		}
		if isSeqItem(l.text) {
			return nil, fmt.Errorf("line %d: a list item where a key was expected", l.no)
		}
		key, rest, ok, err := splitKey(l)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("line %d: expected `key: value`, got %q", l.no, l.text)
		}
		if key == "<<" {
			return nil, fmt.Errorf("line %d: a merge key (<<) is not supported", l.no)
		}
		if containsKey(n.Keys, key) {
			return nil, fmt.Errorf("line %d: duplicate key %q", l.no, key)
		}
		p.pos++
		val, err := p.parseValue(l, rest, indent)
		if err != nil {
			return nil, err
		}
		n.Keys = append(n.Keys, key)
		n.Vals = append(n.Vals, val)
	}
	return n, nil
}

func containsKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// parseValue reads what follows `key:` on line l, whose map sits at indent.
func (p *parser) parseValue(l srcLine, rest string, indent int) (*Node, error) {
	if rest == "" {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			return &Node{Kind: KindNull, Line: l.no}, nil
		}
		next := p.lines[p.pos]
		switch {
		case next.indent > indent:
			return p.parseBlock(next.indent)
		case next.indent == indent && isSeqItem(next.text):
			return p.parseSeq(indent)
		}
		return &Node{Kind: KindNull, Line: l.no}, nil
	}
	if rest[0] == '|' || rest[0] == '>' {
		return p.parseBlockScalar(l, rest, indent)
	}
	n, err := parseInline(rest, l.no)
	if err != nil {
		return nil, err
	}
	if n.Kind == KindScalar {
		p.skipBlank()
		if p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
			return nil, fmt.Errorf("line %d: a scalar continued on the next line is not supported; use a | block", p.lines[p.pos].no)
		}
	}
	return n, nil
}

func (p *parser) parseSeq(indent int) (*Node, error) {
	n := &Node{Kind: KindList, Line: p.lines[p.pos].no}
	for range p.lines {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			return n, nil
		}
		l := p.lines[p.pos]
		if l.indent < indent || isDocMarker(l) || (l.indent == indent && !isSeqItem(l.text)) {
			return n, nil
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", l.no)
		}
		rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		col := indent + (len(l.text) - len(rest))
		switch {
		case rest == "":
			p.pos++
			item, err := p.parseBlock(indent + 1)
			if err != nil {
				return nil, err
			}
			n.Items = append(n.Items, item)
		case isSeqItem(rest):
			return nil, fmt.Errorf("line %d: a list directly inside a list item is not supported", l.no)
		default:
			item, err := p.parseItem(l, rest, col)
			if err != nil {
				return nil, err
			}
			n.Items = append(n.Items, item)
		}
	}
	return n, nil
}

// parseItem reads a list item that starts on the dash's own line: a map whose
// first key sits at col, or a scalar or flow value.
func (p *parser) parseItem(l srcLine, rest string, col int) (*Node, error) {
	virtual := srcLine{no: l.no, indent: col, raw: strings.Repeat(" ", col) + rest, text: rest}
	if _, _, ok, err := splitKey(virtual); err != nil {
		return nil, err
	} else if ok {
		p.lines[p.pos] = virtual
		return p.parseMap(col)
	}
	p.pos++
	return parseInline(rest, l.no)
}

// splitKey reads `key: rest` from a line; ok is false for a line that is not
// a key, and err names a construct this reader refuses.
func splitKey(l srcLine) (key, rest string, ok bool, err error) {
	t := l.text
	if t == "" || t[0] == '[' || t[0] == '{' || t[0] == '|' || t[0] == '>' {
		return "", "", false, nil
	}
	if t[0] == '?' && (len(t) == 1 || t[1] == ' ') {
		return "", "", false, fmt.Errorf("line %d: an explicit key (?) is not supported", l.no)
	}
	if t[0] == '"' || t[0] == '\'' {
		s, end, qerr := parseQuoted(t, l.no)
		if qerr != nil {
			return "", "", false, qerr
		}
		after := strings.TrimLeft(t[end:], " ")
		if !strings.HasPrefix(after, ":") {
			return "", "", false, nil
		}
		return s, strings.TrimLeft(after[1:], " "), true, nil
	}
	for i := 0; i < len(t); i++ {
		if t[i] == ':' && (i+1 == len(t) || t[i+1] == ' ') {
			return t[:i], strings.TrimLeft(t[i+1:], " "), true, nil
		}
	}
	return "", "", false, nil
}

// parseInline reads a one-line value: a quoted or plain scalar, or a flow
// sequence or map.
func parseInline(s string, no int) (*Node, error) {
	switch s[0] {
	case '[', '{':
		n, rest, err := parseFlow(s, no)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(rest) != "" {
			return nil, fmt.Errorf("line %d: unexpected text after the flow value: %q", no, rest)
		}
		return n, nil
	case '"', '\'':
		str, end, err := parseQuoted(s, no)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s[end:]) != "" {
			return nil, fmt.Errorf("line %d: unexpected text after the quoted scalar: %q", no, s[end:])
		}
		return &Node{Kind: KindScalar, Str: str, Quoted: true, Line: no}, nil
	case '&', '*', '!', '@', '`', '%':
		return nil, fmt.Errorf("line %d: %q starts an anchor, alias, tag or directive, which this reader does not support", no, string(s[0]))
	}
	for i := 0; i+1 < len(s); i++ {
		if s[i] == ':' && s[i+1] == ' ' {
			return nil, fmt.Errorf("line %d: a plain scalar may not contain `: `; quote it: %q", no, s)
		}
	}
	if s == "~" || s == "null" {
		return &Node{Kind: KindNull, Line: no}, nil
	}
	return &Node{Kind: KindScalar, Str: s, Line: no}, nil
}

// parseQuoted reads the quoted scalar at the start of s and returns its value
// and the index just past the closing quote.
func parseQuoted(s string, no int) (string, int, error) {
	q := s[0]
	var b strings.Builder
	skip := 0
	for i := 1; i < len(s); i++ {
		if skip > 0 {
			skip--
			continue
		}
		c := s[i]
		switch {
		case q == '\'' && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				skip = 1
				continue
			}
			return b.String(), i + 1, nil
		case q == '"' && c == '"':
			return b.String(), i + 1, nil
		case q == '"' && c == '\\':
			if i+1 >= len(s) {
				return "", 0, fmt.Errorf("line %d: unterminated escape", no)
			}
			r, extra, err := unescape(s[i+1:], no)
			if err != nil {
				return "", 0, err
			}
			b.WriteString(r)
			skip = 1 + extra
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("line %d: a quoted scalar must close on the same line", no)
}

// unescape reads one double-quote escape (the text after the backslash) and
// returns its value and how many extra bytes it consumed.
func unescape(s string, no int) (string, int, error) {
	switch s[0] {
	case 'n':
		return "\n", 0, nil
	case 't':
		return "\t", 0, nil
	case 'r':
		return "\r", 0, nil
	case '0':
		return "\x00", 0, nil
	case '"', '\\', '/', ' ':
		return s[:1], 0, nil
	case 'x', 'u':
		width := 2
		if s[0] == 'u' {
			width = 4
		}
		if len(s) < 1+width {
			return "", 0, fmt.Errorf("line %d: short \\%c escape", no, s[0])
		}
		v, err := strconv.ParseUint(s[1:1+width], 16, 32)
		if err != nil {
			return "", 0, fmt.Errorf("line %d: bad \\%c escape", no, s[0])
		}
		return string(rune(v)), width, nil
	}
	return "", 0, fmt.Errorf("line %d: unsupported escape \\%c", no, s[0])
}

// parseBlockScalar reads a | or > scalar whose content lines are indented
// deeper than the map that holds its key (parentIndent).
func (p *parser) parseBlockScalar(l srcLine, header string, parentIndent int) (*Node, error) {
	folded := header[0] == '>'
	chomp := ""
	for _, c := range header[1:] {
		switch c {
		case '-', '+':
			chomp = string(c)
		default:
			return nil, fmt.Errorf("line %d: block scalar header %q is not supported", l.no, header)
		}
	}
	var body []string
	contentIndent := -1
	for p.pos < len(p.lines) {
		raw := p.lines[p.pos]
		if strings.TrimSpace(raw.raw) != "" {
			if raw.indent <= parentIndent {
				break
			}
			if contentIndent == -1 {
				contentIndent = raw.indent
			}
			if raw.indent < contentIndent {
				break
			}
		}
		body = append(body, raw.raw)
		p.pos++
	}
	prefix := strings.Repeat(" ", max(contentIndent, 0))
	for i, b := range body {
		if strings.TrimSpace(b) == "" {
			body[i] = ""
		} else {
			body[i] = strings.TrimPrefix(b, prefix)
		}
	}
	trailing := 0
	// walk-terminates: each turn drops one trailing blank line from body
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
		trailing++
	}
	text := joinBlock(body, folded)
	switch {
	case chomp == "-" || len(body) == 0:
	case chomp == "+":
		text += strings.Repeat("\n", 1+trailing)
	default:
		text += "\n"
	}
	return &Node{Kind: KindScalar, Str: text, Line: l.no}, nil
}

func joinBlock(body []string, folded bool) string {
	if !folded {
		return strings.Join(body, "\n")
	}
	var b strings.Builder
	for i, line := range body {
		if i > 0 {
			prev := body[i-1]
			switch {
			case line == "":
				b.WriteByte('\n')
			case prev == "":
			case strings.HasPrefix(line, " ") || strings.HasPrefix(prev, " "):
				b.WriteByte('\n')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString(line)
	}
	return b.String()
}
