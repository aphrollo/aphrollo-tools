package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// A baseline file records a CEILING per offending key. Measured above it is a
// regression; measured below it is tightened — lowered or dropped — by the run
// that observed it, and written back atomically. It never rises and never
// gains a key: the only way to admit a new hit is the law's own escape comment.
// That one-way property is the whole point, so the semantics here are a
// deliberate port of the Rust original this engine replaces, tests included.
type Form int

const (
	// Counted is `<key> | <count>`, one line per key: the shape for a law keyed
	// by file. A repeated key is a parse error, never silently summed — a
	// botched merge must not inflate a ceiling by accident.
	Counted Form = iota
	// Multiset is a bare `<key>` (which may itself contain `|`), one line per
	// OCCURRENCE: the shape for identity-keyed laws, where repetition IS the
	// count and a duplicate line is normal.
	Multiset
)

type baselineLine struct {
	verbatim string // a `#` comment or blank line, kept exactly as read
	data     bool
	key      string
	count    int
}

// Baseline is one parsed baseline file: comment/blank lines and keyed ceilings,
// both in original file order.
type Baseline struct {
	form  Form
	lines []baselineLine
	path  string
}

// Change is one key's ceiling movement: `To` is 0 for a removal.
type Change struct {
	Key      string
	From, To int
}

// Tightening is what Tighten changed.
type Tightening struct {
	Lowered []Change
	Removed []Change
}

func (t Tightening) Changed() bool { return len(t.Lowered) > 0 || len(t.Removed) > 0 }

// Regression is a key whose measured count exceeds its recorded ceiling.
type Regression struct {
	Key      string `json:"key"`
	Baseline int    `json:"baseline"`
	Measured int    `json:"measured"`
}

// ParseBaseline reads text in the given form.
func ParseBaseline(text string, form Form) (*Baseline, error) {
	b := &Baseline{form: form}
	seen := map[string]int{}
	for i, raw := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if text == "" {
				continue
			}
			b.lines = append(b.lines, baselineLine{verbatim: raw})
			continue
		}
		if form == Multiset {
			b.lines = append(b.lines, baselineLine{data: true, key: trimmed, count: 1})
			continue
		}
		key, countText, ok := strings.Cut(trimmed, " | ")
		if !ok {
			return nil, fmt.Errorf("line %d: a counted baseline line is `<key> | <count>`: %s", lineNo, trimmed)
		}
		count, err := strconv.Atoi(strings.TrimSpace(countText))
		if err != nil {
			return nil, fmt.Errorf("line %d: a baseline count is an integer: %s", lineNo, trimmed)
		}
		key = strings.TrimSpace(key)
		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf(
				"duplicate key %q at lines %d and %d — a counted baseline entry is unique, never silently summed",
				key, first, lineNo)
		}
		seen[key] = lineNo
		b.lines = append(b.lines, baselineLine{data: true, key: key, count: count})
	}
	return b, nil
}

// LoadBaseline reads a baseline file; an absent file is an EMPTY baseline, so
// a law lands with a bar of zero rather than silently unenforced.
func LoadBaseline(path string, form Form) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b := &Baseline{form: form, path: path}
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	b, err := ParseBaseline(string(data), form)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b.path = path
	return b, nil
}

// Counts is the per-key ceiling, tallied across occurrences.
func (b *Baseline) Counts() map[string]int {
	out := map[string]int{}
	for _, l := range b.lines {
		if l.data {
			out[l.key] += l.count
		}
	}
	return out
}

// Regressions names every key whose measured count exceeds its ceiling (0 for
// a key the baseline has never seen), sorted for stable output.
func (b *Baseline) Regressions(measured map[string]int) []Regression {
	ceiling := b.Counts()
	keys := map[string]bool{}
	for k := range ceiling {
		keys[k] = true
	}
	for k := range measured {
		keys[k] = true
	}
	var out []Regression
	for k := range keys {
		if m := measured[k]; m > ceiling[k] {
			out = append(out, Regression{Key: k, Baseline: ceiling[k], Measured: m})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Tighten lowers every key whose measured count sits below its ceiling and
// drops keys the scan no longer names. It never raises a count and never adds
// a key: a key above its ceiling is a Regressions finding, and this clamps to
// the existing ceiling instead of touching it.
func (b *Baseline) Tighten(measured map[string]int) Tightening {
	before := b.Counts()
	target := map[string]int{}
	for k, old := range before {
		target[k] = min(old, measured[k])
	}

	var t Tightening
	for _, k := range sortedKeys(before) {
		old, now := before[k], target[k]
		switch {
		case now == 0:
			t.Removed = append(t.Removed, Change{Key: k, From: old})
		case now < old:
			t.Lowered = append(t.Lowered, Change{Key: k, From: old, To: now})
		}
	}

	kept := b.lines[:0]
	seen := map[string]int{}
	for _, l := range b.lines {
		if !l.data {
			kept = append(kept, l)
			continue
		}
		keepUpTo := target[l.key]
		if seen[l.key] >= keepUpTo {
			seen[l.key]++
			continue
		}
		seen[l.key]++
		if b.form == Counted {
			l.count = keepUpTo
		}
		kept = append(kept, l)
	}
	b.lines = kept
	return t
}

// Render is the file's text, always LF-terminated; WriteIfChanged matches it
// to whatever line ending is already on disk.
func (b *Baseline) Render() string {
	var sb strings.Builder
	for _, l := range b.lines {
		switch {
		case !l.data:
			sb.WriteString(l.verbatim)
		case b.form == Counted:
			fmt.Fprintf(&sb, "%s | %d", l.key, l.count)
		default:
			sb.WriteString(l.key)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// WriteIfChanged writes path iff the rendered text differs from what is there:
// byte-stable on unchanged input (CRLF checkouts included) and atomic —
// `<name>.tmp.<pid>` then a rename, with the tmp file removed before any error
// propagates.
func (b *Baseline) WriteIfChanged(path string) (bool, error) {
	existing, err := os.ReadFile(path)
	hadFile := err == nil
	rendered := b.Render()
	if hadFile && strings.Contains(string(existing), "\r\n") {
		rendered = strings.ReplaceAll(rendered, "\n", "\r\n")
	}
	if hadFile && string(existing) == rendered {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + fmt.Sprintf(".tmp.%d", os.Getpid())
	if err := os.WriteFile(tmp, []byte(rendered), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// Path is the file this baseline was loaded from ("" when parsed from text).
func (b *Baseline) Path() string { return b.path }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
