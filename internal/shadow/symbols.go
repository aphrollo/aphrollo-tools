package shadow

import (
	"os"
	"regexp"
	"strings"
	"unicode"
)

// AddsSymbol is the kernel's Event.AddsSymbol for a write about to happen: whether it
// adds an exported symbol or a new function, which makes the code it writes
// "untested code" however well covered its unit is. It is read from the payload
// itself, for Go files only (every other language answers false, which the shadow
// notes say):
//
//   - Edit and MultiEdit: the top-level func, type, var and const names the old text
//     declares against those the new text declares. A fragment is enough; no file is
//     parsed.
//   - Write: the names of the content against those of the file on disk (one read; a
//     file not there declares none).
//
// A new func of any name counts, a new type, var or const only when it is exported.
func AddsSymbol(p Payload, file string) bool {
	if !strings.HasSuffix(strings.ToLower(file), ".go") {
		return false
	}
	switch p.ToolName {
	case "Edit":
		return newSymbol(declaredNames(p.ToolInput.OldString), declaredNames(p.ToolInput.NewString))
	case "MultiEdit":
		var old, now strings.Builder
		for _, e := range p.ToolInput.Edits {
			old.WriteString(e.OldString + "\n")
			now.WriteString(e.NewString + "\n")
		}
		return newSymbol(declaredNames(old.String()), declaredNames(now.String()))
	case "Write":
		disk, _ := os.ReadFile(file) // a file not there declares no name
		return newSymbol(declaredNames(string(disk)), declaredNames(p.ToolInput.Content))
	}
	return false
}

var (
	funcDecl  = regexp.MustCompile(`^func\s+(?:\(\s*\w*\s*\*?\s*(\w+)[^)]*\)\s*)?(\w+)`)
	genDecl   = regexp.MustCompile(`^(var|const|type)\s+(\w+)`)
	groupOpen = regexp.MustCompile(`^(var|const|type)\s*\(\s*$`)
	groupName = regexp.MustCompile(`^[ \t]+(\w+)`)
)

// declaredNames are the top-level names a Go text declares, keyed by kind: "func:F",
// "func:T.M" for a method, "var:V", "const:C", "type:T". A name in a grouped
// var, const or type block counts as its own declaration.
func declaredNames(src string) map[string]bool {
	out := map[string]bool{}
	group := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		if group != "" {
			if strings.HasPrefix(line, ")") {
				group = ""
			} else if m := groupName.FindStringSubmatch(line); m != nil {
				out[group+":"+m[1]] = true
			}
			continue
		}
		switch {
		case funcDecl.MatchString(line):
			m := funcDecl.FindStringSubmatch(line)
			if m[1] != "" {
				out["func:"+m[1]+"."+m[2]] = true
			} else {
				out["func:"+m[2]] = true
			}
		case groupOpen.MatchString(line):
			group = groupOpen.FindStringSubmatch(line)[1]
		case genDecl.MatchString(line):
			m := genDecl.FindStringSubmatch(line)
			out[m[1]+":"+m[2]] = true
		}
	}
	return out
}

// newSymbol reports whether now declares a func it did not declare before, or an
// exported type, var or const.
func newSymbol(before, now map[string]bool) bool {
	for k := range now {
		if before[k] {
			continue
		}
		kind, name, _ := strings.Cut(k, ":")
		if kind == "func" {
			return true
		}
		if r := []rune(name); len(r) > 0 && unicode.IsUpper(r[0]) {
			return true
		}
	}
	return false
}
