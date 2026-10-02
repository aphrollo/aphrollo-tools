package ghworkflow

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func (p *exprParser) apply(name string, a []any) (any, error) {
	switch strings.ToLower(name) {
	case "always":
		p.used = true
		return true, nil
	case "success":
		p.used = true
		return p.sc.Status == "success", nil
	case "failure":
		p.used = true
		return p.sc.Status == "failure", nil
	case "cancelled":
		p.used = true
		return false, nil
	case "hashfiles":
		p.sc.note("hashFiles is not computed locally (read as empty)")
		return "", nil
	}
	return p.applyData(name, a)
}

// applyData is the functions that compute from their arguments.
func (p *exprParser) applyData(name string, a []any) (any, error) {
	lower := strings.ToLower(name)
	want := map[string]int{"contains": 2, "startswith": 2, "endswith": 2, "format": 1, "join": 1, "tojson": 1, "fromjson": 1}
	n, known := want[lower]
	if !known {
		return nil, fmt.Errorf("the function %s is not supported", name)
	}
	if len(a) < n {
		return nil, fmt.Errorf("%s needs %d argument(s)", name, n)
	}
	switch lower {
	case "contains":
		if list, ok := a[0].([]any); ok {
			for _, e := range list {
				if looseEqual(e, a[1]) {
					return true, nil
				}
			}
			return false, nil
		}
		return strings.Contains(strings.ToLower(text(a[0])), strings.ToLower(text(a[1]))), nil
	case "startswith":
		return strings.HasPrefix(strings.ToLower(text(a[0])), strings.ToLower(text(a[1]))), nil
	case "endswith":
		return strings.HasSuffix(strings.ToLower(text(a[0])), strings.ToLower(text(a[1]))), nil
	case "format":
		out := text(a[0])
		for i, v := range a[1:] {
			out = strings.ReplaceAll(out, "{"+strconv.Itoa(i)+"}", text(v))
		}
		return out, nil
	case "join":
		sep := ","
		if len(a) > 1 {
			sep = text(a[1])
		}
		list, ok := a[0].([]any)
		if !ok {
			return text(a[0]), nil
		}
		parts := make([]string, len(list))
		for i, e := range list {
			parts[i] = text(e)
		}
		return strings.Join(parts, sep), nil
	case "tojson":
		b, err := json.MarshalIndent(a[0], "", "  ")
		return string(b), err
	}
	var v any
	if err := json.Unmarshal([]byte(text(a[0])), &v); err != nil {
		return nil, fmt.Errorf("fromJSON: %w", err)
	}
	return v, nil
}

// index reads key from a map (case-insensitively, as GitHub does) or a list.
func index(base, key any) (any, bool) {
	switch b := base.(type) {
	case map[string]any:
		k := text(key)
		if v, ok := b[k]; ok {
			return v, true
		}
		for name, v := range b {
			if strings.EqualFold(name, k) {
				return v, true
			}
		}
	case []any:
		if f, ok := key.(float64); ok && f >= 0 && int(f) < len(b) {
			return b[int(f)], true
		}
		if s, ok := key.(string); ok {
			// `.*` yields lists; a property read on one reads it on each element.
			out := make([]any, 0, len(b))
			for _, e := range b {
				v, _ := index(e, s)
				out = append(out, v)
			}
			return out, true
		}
	}
	return nil, false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// text is a value as it renders into a string.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func number(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		if strings.TrimSpace(x) == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return math.NaN()
}

func looseEqual(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return strings.EqualFold(as, bs)
	}
	return number(a) == number(b)
}

func compare(op string, a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	var c int
	if aok && bok {
		c = strings.Compare(strings.ToLower(as), strings.ToLower(bs))
	} else {
		x, y := number(a), number(b)
		switch {
		case math.IsNaN(x) || math.IsNaN(y):
			return false
		case x < y:
			c = -1
		case x > y:
			c = 1
		}
	}
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0
}

// Interpolate replaces every ${{ expression }} in s with its value.
func (s *Scope) Interpolate(src string) (string, error) {
	var out strings.Builder
	rest := src
	for range src {
		start := strings.Index(rest, "${{")
		if start < 0 {
			break
		}
		out.WriteString(rest[:start])
		end := closeIndex(rest[start+3:])
		if end < 0 {
			return "", fmt.Errorf("unterminated ${{ in %q", src)
		}
		expr := strings.TrimSpace(rest[start+3 : start+3+end])
		v, _, err := s.Eval(expr)
		if err != nil {
			return "", fmt.Errorf("${{ %s }}: %w", expr, err)
		}
		out.WriteString(text(v))
		rest = rest[start+3+end+2:]
	}
	out.WriteString(rest)
	return out.String(), nil
}

// closeIndex finds the }} that ends an expression, skipping quoted strings.
func closeIndex(s string) int {
	inQuote := false
	for i := 0; i+1 < len(s); i++ {
		switch {
		case s[i] == '\'':
			inQuote = !inQuote
		case !inQuote && s[i] == '}' && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// Cond decides an if: expression. An empty one is the implicit success().
// An error means the expression could not be evaluated; the caller says so and
// runs the step anyway.
func (s *Scope) Cond(expr string) (bool, error) {
	e := strings.TrimSpace(expr)
	if e == "" {
		return s.Status == "success", nil
	}
	if strings.Contains(e, "${{") {
		end := -1
		if strings.HasPrefix(e, "${{") {
			end = closeIndex(e[3:])
		}
		if end < 0 || strings.TrimSpace(e[3+end+2:]) != "" {
			str, err := s.Interpolate(e)
			return truthy(str) && s.Status == "success", err
		}
		e = strings.TrimSpace(e[3 : 3+end])
	}
	v, used, err := s.Eval(e)
	if err != nil {
		return false, err
	}
	if !used {
		return s.Status == "success" && truthy(v), nil
	}
	return truthy(v), nil
}
