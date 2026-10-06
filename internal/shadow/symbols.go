package shadow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// AddsSymbol is the kernel's Event.AddsSymbol for a write about to happen: whether it
// adds an exported symbol or a new function, which makes the code it writes
// "untested code" however well covered its unit is. It is read from the payload
// itself, for Go, Python and TypeScript or JavaScript files (every other language
// answers false, which the shadow notes say):
//
//   - Edit and MultiEdit: the names the old text declares against those the new text
//     declares. A fragment is enough; no file is parsed.
//   - Write: the names of the content against those of the file on disk (one read; a
//     file not there declares none).
//
// Go: a new func of any name counts, a new type, var or const only when it is
// exported. Python: a new def of any name (a method too), a new class unless its
// name starts with an underscore. TypeScript and JavaScript: a new function of any
// name (a const bound to an arrow function too), a new exported declaration of any
// kind. None of the three is parsed: the gate holds no symbol table for them, so a
// declaration is a line the language's own syntax opens with.
func AddsSymbol(p Payload, file string) bool {
	names, fresh := declaredNames, newSymbol
	switch strings.ToLower(filepath.Ext(file)) {
	case ".go":
	case ".py":
		names, fresh = pyDeclaredNames, anyNewName
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		names, fresh = nodeDeclaredNames, anyNewName
	default:
		return false
	}
	switch p.ToolName {
	case "Edit":
		return fresh(names(p.ToolInput.OldString), names(p.ToolInput.NewString))
	case "MultiEdit":
		var old, now strings.Builder
		for _, e := range p.ToolInput.Edits {
			old.WriteString(e.OldString + "\n")
			now.WriteString(e.NewString + "\n")
		}
		return fresh(names(old.String()), names(now.String()))
	case "Write":
		disk, _ := os.ReadFile(file) // a file not there declares no name
		return fresh(names(string(disk)), names(p.ToolInput.Content))
	}
	return false
}

var (
	pyDef   = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	pyClass = regexp.MustCompile(`^\s*class\s+([A-Za-z]\w*)`)

	nodeFunc   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*(\w+)`)
	nodeArrow  = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|\w+)\s*(?::\s*[^=]+)?=>`)
	nodeExport = regexp.MustCompile(`^export\s+(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:class|interface|type|enum|const|let|var)\s+(\w+)`)
)

// pyDeclaredNames are the names a Python text declares: "func:name" for a def at any
// depth, "class:Name" for a class whose name is public.
func pyDeclaredNames(src string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := pyDef.FindStringSubmatch(line); m != nil {
			out["func:"+m[1]] = true
		} else if m := pyClass.FindStringSubmatch(line); m != nil {
			out["class:"+m[1]] = true
		}
	}
	return out
}

// nodeDeclaredNames are the names a TypeScript or JavaScript text declares: "func:name"
// for a function or a binding of an arrow function, "export:name" for a top-level
// exported declaration.
func nodeDeclaredNames(src string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case nodeFunc.MatchString(line):
			out["func:"+nodeFunc.FindStringSubmatch(line)[1]] = true
		case nodeArrow.MatchString(line):
			out["func:"+nodeArrow.FindStringSubmatch(line)[1]] = true
		}
		if m := nodeExport.FindStringSubmatch(line); m != nil {
			out["export:"+m[1]] = true
		}
	}
	return out
}

// anyNewName reports whether now declares a name before did not: the names of the
// Python and node readers are only the ones that count.
func anyNewName(before, now map[string]bool) bool {
	for k := range now {
		if !before[k] {
			return true
		}
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
