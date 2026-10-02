package ghworkflow

import (
	"fmt"
	"strings"
)

// parseFlow reads a flow sequence or map of scalars (and nested flow values)
// from the start of s, returning the node and the text after it.
func parseFlow(s string, no int) (*Node, string, error) {
	closer := byte(']')
	n := &Node{Kind: KindList, Line: no}
	if s[0] == '{' {
		closer, n.Kind = '}', KindMap
	}
	rest := strings.TrimLeft(s[1:], " ")
	for range s {
		if len(rest) == 0 || rest[0] == closer {
			break
		}
		var item *Node
		var key string
		var err error
		if n.Kind == KindMap {
			key, rest, err = flowKey(rest, no)
			if err != nil {
				return nil, "", err
			}
		}
		item, rest, err = flowValue(rest, no, closer)
		if err != nil {
			return nil, "", err
		}
		if n.Kind == KindMap {
			n.Keys, n.Vals = append(n.Keys, key), append(n.Vals, item)
		} else {
			n.Items = append(n.Items, item)
		}
		rest = strings.TrimLeft(rest, " ")
		if strings.HasPrefix(rest, ",") {
			rest = strings.TrimLeft(rest[1:], " ")
		} else if len(rest) > 0 && rest[0] != closer {
			return nil, "", fmt.Errorf("line %d: expected `,` or `%c` in the flow value near %q", no, closer, rest)
		}
	}
	if len(rest) == 0 {
		return nil, "", fmt.Errorf("line %d: a flow value must close on the same line", no)
	}
	return n, rest[1:], nil
}

func flowKey(s string, no int) (string, string, error) {
	if s[0] == '"' || s[0] == '\'' {
		k, end, err := parseQuoted(s, no)
		if err != nil {
			return "", "", err
		}
		after := strings.TrimLeft(s[end:], " ")
		if !strings.HasPrefix(after, ":") {
			return "", "", fmt.Errorf("line %d: expected `:` after the flow key %q", no, k)
		}
		return k, strings.TrimLeft(after[1:], " "), nil
	}
	i := strings.IndexAny(s, ":,}")
	if i < 0 || s[i] != ':' {
		return "", "", fmt.Errorf("line %d: expected `key: value` in the flow map near %q", no, s)
	}
	return strings.TrimSpace(s[:i]), strings.TrimLeft(s[i+1:], " "), nil
}

func flowValue(s string, no int, closer byte) (*Node, string, error) {
	if s == "" {
		return nil, "", fmt.Errorf("line %d: a flow value must close on the same line", no)
	}
	switch s[0] {
	case '[', '{':
		return parseFlow(s, no)
	case '"', '\'':
		str, end, err := parseQuoted(s, no)
		if err != nil {
			return nil, "", err
		}
		return &Node{Kind: KindScalar, Str: str, Quoted: true, Line: no}, s[end:], nil
	case '&', '*', '!':
		return nil, "", fmt.Errorf("line %d: anchors, aliases and tags are not supported", no)
	}
	i := strings.IndexAny(s, ",]}")
	if i < 0 {
		return nil, "", fmt.Errorf("line %d: a flow value must close on the same line", no)
	}
	if s[i] != ',' && s[i] != closer {
		return nil, "", fmt.Errorf("line %d: mismatched bracket in the flow value near %q", no, s)
	}
	return &Node{Kind: KindScalar, Str: strings.TrimSpace(s[:i]), Line: no}, s[i:], nil
}
