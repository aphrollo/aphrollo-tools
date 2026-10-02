package ghworkflow

import (
	"fmt"
	"strconv"
	"strings"
)

// Expressions: the part of GitHub's expression language workflows use to pick
// steps and fill env values: contexts (github, env, steps, needs, matrix, ...),
// string, number, boolean and null literals, ! && || == != < <= > >=,
// parentheses, property and index access (including the .* object filter),
// and the functions always, success, failure, cancelled, contains, startsWith,
// endsWith, format, join, toJSON, fromJSON and hashFiles. A value is nil, bool,
// float64, string, []any or map[string]any.

// Scope is what an expression reads: the context maps, and the job status the
// status functions answer from.
type Scope struct {
	Root   map[string]any
	Status string // success | failure | cancelled
	Notes  []string
}

// note records an unresolved lookup once, so output can say what was empty.
func (s *Scope) note(msg string) {
	for _, n := range s.Notes {
		if n == msg {
			return
		}
	}
	s.Notes = append(s.Notes, msg)
}

type token struct {
	kind string // str num id op eof
	text string
	num  float64
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for range len(src) {
		if i >= len(src) {
			break
		}
		c := src[i]
		switch {
		case isSpace(c):
			i++
		case c == '\'':
			str, end, ok := scanQuoted(src, i)
			if !ok {
				return nil, fmt.Errorf("unterminated string in expression %q", src)
			}
			toks = append(toks, token{kind: "str", text: str})
			i = end
		case isDigit(c):
			end := scanWhile(src, i, func(b byte) bool { return isDigit(b) || b == '.' })
			f, err := strconv.ParseFloat(src[i:end], 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q in expression", src[i:end])
			}
			toks = append(toks, token{kind: "num", num: f})
			i = end
		case isIdentStart(c):
			end := scanWhile(src, i, func(b byte) bool { return isIdentStart(b) || isDigit(b) || b == '-' })
			toks = append(toks, token{kind: "id", text: src[i:end]})
			i = end
		default:
			op := string(c)
			if i+1 < len(src) {
				if two := src[i : i+2]; two == "&&" || two == "||" || two == "==" || two == "!=" || two == "<=" || two == ">=" {
					op = two
				}
			}
			if !strings.Contains("&&|||==!=<=>=!<>()[],.*", op) {
				return nil, fmt.Errorf("unexpected %q in expression %q", op, src)
			}
			toks = append(toks, token{kind: "op", text: op})
			i += len(op)
		}
	}
	return append(toks, token{kind: "eof"}), nil
}

// scanWhile is the index of the first byte at or after from that pred rejects,
// or len(src). It is bounded by the length of src by construction.
func scanWhile(src string, from int, pred func(byte) bool) int {
	for k := range len(src) - from {
		if !pred(src[from+k]) {
			return from + k
		}
	}
	return len(src)
}

// scanQuoted reads the single-quoted string starting at src[start] (” is a
// quote) and returns its value and the index past the closing quote.
func scanQuoted(src string, start int) (string, int, bool) {
	var b strings.Builder
	rest := src[start+1:]
	skip := false
	for k := range len(rest) {
		if skip {
			skip = false
			continue
		}
		if rest[k] != '\'' {
			b.WriteByte(rest[k])
			continue
		}
		if k+1 < len(rest) && rest[k+1] == '\'' {
			b.WriteByte('\'')
			skip = true
			continue
		}
		return b.String(), start + 1 + k + 1, true
	}
	return "", 0, false
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

type exprParser struct {
	toks []token
	pos  int
	sc   *Scope
	used bool // a status function was called
	// lastPath is the dotted path of the identifier primary just read, so a
	// property lookup that misses can name the whole path.
	lastPath string
}

type thunk func() (any, error)

// Eval evaluates one expression. usedStatus reports whether it called a
// status function (always, success, failure, cancelled), which switches off
// the implicit success() an if: gets.
func (s *Scope) Eval(src string) (v any, usedStatus bool, err error) {
	toks, err := lex(src)
	if err != nil {
		return nil, false, err
	}
	p := &exprParser{toks: toks, sc: s}
	t, err := p.or()
	if err != nil {
		return nil, false, err
	}
	if p.peek().kind != "eof" {
		return nil, false, fmt.Errorf("unexpected %q in expression %q", p.peek().text, src)
	}
	v, err = t()
	return v, p.used, err
}

func (p *exprParser) peek() token { return p.toks[p.pos] }

func (p *exprParser) accept(op string) bool {
	if t := p.peek(); t.kind == "op" && t.text == op {
		p.pos++
		return true
	}
	return false
}

func (p *exprParser) or() (thunk, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for range p.toks {
		if !p.accept("||") {
			break
		}
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = func(l, r thunk) thunk {
			return func() (any, error) {
				v, err := l()
				if err != nil || truthy(v) {
					return v, err
				}
				return r()
			}
		}(l, r)
	}
	return l, nil
}

func (p *exprParser) and() (thunk, error) {
	l, err := p.eq()
	if err != nil {
		return nil, err
	}
	for range p.toks {
		if !p.accept("&&") {
			break
		}
		r, err := p.eq()
		if err != nil {
			return nil, err
		}
		l = func(l, r thunk) thunk {
			return func() (any, error) {
				v, err := l()
				if err != nil || !truthy(v) {
					return v, err
				}
				return r()
			}
		}(l, r)
	}
	return l, nil
}

func (p *exprParser) eq() (thunk, error) {
	l, err := p.rel()
	if err != nil {
		return nil, err
	}
	for range p.toks {
		op := ""
		if p.accept("==") {
			op = "=="
		} else if p.accept("!=") {
			op = "!="
		} else {
			break
		}
		r, err := p.rel()
		if err != nil {
			return nil, err
		}
		l = binary(l, r, func(a, b any) (any, error) { return looseEqual(a, b) == (op == "=="), nil })
	}
	return l, nil
}

func (p *exprParser) rel() (thunk, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if !p.accept(op) {
			continue
		}
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		op := op
		return binary(l, r, func(a, b any) (any, error) { return compare(op, a, b), nil }), nil
	}
	return l, nil
}

func binary(l, r thunk, f func(a, b any) (any, error)) thunk {
	return func() (any, error) {
		a, err := l()
		if err != nil {
			return nil, err
		}
		b, err := r()
		if err != nil {
			return nil, err
		}
		return f(a, b)
	}
}

func (p *exprParser) unary() (thunk, error) {
	if p.accept("!") {
		t, err := p.unary()
		if err != nil {
			return nil, err
		}
		return func() (any, error) {
			v, err := t()
			return !truthy(v), err
		}, nil
	}
	return p.postfix()
}

func (p *exprParser) postfix() (thunk, error) {
	p.lastPath = ""
	t, err := p.primary()
	if err != nil {
		return nil, err
	}
	path := p.lastPath
	for range p.toks {
		switch {
		case p.accept("."):
			if p.accept("*") {
				t = filterAll(t)
				continue
			}
			id := p.peek()
			if id.kind != "id" {
				return nil, fmt.Errorf("expected a property name after `.`")
			}
			p.pos++
			path += "." + id.text
			t = p.property(t, func() (any, error) { return id.text, nil }, path)
		case p.accept("["):
			idx, err := p.or()
			if err != nil {
				return nil, err
			}
			if !p.accept("]") {
				return nil, fmt.Errorf("expected `]`")
			}
			path += "[]"
			t = p.property(t, idx, path)
		default:
			return t, nil
		}
	}
	return t, nil
}

func (p *exprParser) property(base, key thunk, label string) thunk {
	return func() (any, error) {
		b, err := base()
		if err != nil {
			return nil, err
		}
		k, err := key()
		if err != nil {
			return nil, err
		}
		v, ok := index(b, k)
		if !ok && b != nil {
			p.sc.note("unresolved: " + label + " (read as empty)")
		}
		return v, nil
	}
}

// filterAll is the .* object filter: the values of a map (or the elements of
// a list), so the next property is read from each.
func filterAll(base thunk) thunk {
	return func() (any, error) {
		b, err := base()
		if err != nil {
			return nil, err
		}
		switch m := b.(type) {
		case map[string]any:
			out := make([]any, 0, len(m))
			for _, k := range sortedKeys(m) {
				out = append(out, m[k])
			}
			return out, nil
		case []any:
			return m, nil
		}
		return []any{}, nil
	}
}

func (p *exprParser) primary() (thunk, error) {
	t := p.peek()
	p.pos++
	switch {
	case t.kind == "str":
		return func() (any, error) { return t.text, nil }, nil
	case t.kind == "num":
		return func() (any, error) { return t.num, nil }, nil
	case t.kind == "op" && t.text == "(":
		inner, err := p.or()
		if err != nil {
			return nil, err
		}
		if !p.accept(")") {
			return nil, fmt.Errorf("expected `)`")
		}
		return inner, nil
	case t.kind == "id":
		switch t.text {
		case "true":
			return func() (any, error) { return true, nil }, nil
		case "false":
			return func() (any, error) { return false, nil }, nil
		case "null":
			return func() (any, error) { return nil, nil }, nil
		}
		if p.accept("(") {
			return p.call(t.text)
		}
		p.lastPath = t.text
		return func() (any, error) {
			v, ok := index(p.sc.Root, t.text)
			if !ok {
				p.sc.note("unresolved: " + t.text + " (read as empty)")
			}
			return v, nil
		}, nil
	}
	return nil, fmt.Errorf("unexpected %q in expression", t.text)
}

func (p *exprParser) call(name string) (thunk, error) {
	var args []thunk
	closed := false
	for range p.toks {
		if p.accept(")") {
			closed = true
			break
		}
		a, err := p.or()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if !p.accept(",") && p.peek().text != ")" {
			return nil, fmt.Errorf("expected `,` or `)` in the call to %s", name)
		}
	}
	if !closed {
		return nil, fmt.Errorf("unterminated call to %s", name)
	}
	return func() (any, error) {
		vals := make([]any, len(args))
		for i, a := range args {
			v, err := a()
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		return p.apply(name, vals)
	}, nil
}
