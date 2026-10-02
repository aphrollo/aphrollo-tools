package argvbatch

import "strings"

// soloGoTestFlags are the `go test` flags that stand alone: they take no
// value, so the word after one is never theirs. A flag that takes a value
// (-shuffle, -count) is self-contained only in its `-name=value` spelling.
var soloGoTestFlags = map[string]bool{
	"-race": true, "-json": true, "-v": true, "-short": true, "-failfast": true,
}

// GoTestPackages is the package list a `go test` line names, in order, and a
// builder that puts any subset of it back on the same line with the verb and
// the flags around it kept. It answers (nil, nil) for any line it cannot
// rebuild without guessing: a command that is not `go test`, a line naming no
// package, and a line carrying a word that is neither a package, the verb, nor
// a flag that stands alone or carries its value after `=`. A flag that takes
// its value as the next word (`-run X`, `-o ./out`) leaves that word's
// meaning to the flag, so a path-looking value could be taken for a package
// and run twice or not at all.
func GoTestPackages(cmd string, args []string) (pkgs []string, withPackages func(pkgs []string) []string) {
	if commandName(cmd) != "go" || len(args) == 0 || args[0] != "test" {
		return nil, nil
	}
	items, first := listItems("go", args)
	if len(items) == 0 {
		return nil, nil
	}
	inItem := make(map[int]bool)
	for _, it := range items {
		for _, i := range it.at {
			inItem[i] = true
			pkgs = append(pkgs, args[i])
		}
	}
	head := args[:first]
	for _, a := range head[1:] {
		if !selfContainedGoTestFlag(a) {
			return nil, nil
		}
	}
	var tail []string
	for i := first; i < len(args); i++ {
		if inItem[i] {
			continue
		}
		if !selfContainedGoTestFlag(args[i]) {
			return nil, nil
		}
		tail = append(tail, args[i])
	}
	return pkgs, func(subset []string) []string {
		run := append([]string{}, head...)
		run = append(run, subset...)
		return append(run, tail...)
	}
}

// selfContainedGoTestFlag is whether a is a flag whose whole meaning is in
// the one word: a stand-alone switch, or `-name=value`.
func selfContainedGoTestFlag(a string) bool {
	if !strings.HasPrefix(a, "-") {
		return false
	}
	return soloGoTestFlags[a] || strings.Contains(a, "=")
}
