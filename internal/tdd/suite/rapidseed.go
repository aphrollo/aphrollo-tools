package suite

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// importFlagRule is a `go test` flag a package takes only when its test
// binary imports a given package, as data: the flag is a registration of that
// import, so handing it to a binary without the import fails the package with
// "flag provided but not defined". A new such flag is one more row.
type importFlagRule struct {
	// Import is the quoted import path as it reads in a Go source file.
	Import string
	// Name is the flag's name, to leave a caller's own value alone.
	Name string
	// Flag is the argument to add, given the seed derived from the content of
	// the packages that import it.
	Flag func(seed uint64) string
}

// importFlagRules is rapid's seed. The gate derives it from the content of the
// packages it applies to, so the same tree gives the same verdict and a new
// commit tries new inputs; CI and a plain `go test` never pass it and stay
// random. A rapid failure prints its own `-rapid.seed=N` line, which the
// output keeps. A seed of 0 means "random" to rapid, so the derived one is
// never 0.
var importFlagRules = []importFlagRule{
	{
		Import: `"pgregory.net/rapid"`,
		Name:   "-rapid.seed",
		Flag:   func(seed uint64) string { return fmt.Sprintf("-rapid.seed=%d", seed) },
	},
}

// splitRapidRuns is r, a `go test` run over plain "./dir" packages, as the
// runs that give the import flag only to the packages that import it: the
// flagged run first, then the rest unflagged. It answers r alone, as the one
// run it was, whenever it cannot split without guessing: not a `go test`, a
// package list it cannot read back as plain "./dir" packages (the whole
// module, a "..." pattern), a seed already named, or no package importing.
func splitRapidRuns(r Runner, root string) []Runner {
	if !isGoTestInvocation(r.Cmd, r.Args) {
		return []Runner{r}
	}
	pkgs, with := argvbatch.GoTestPackages(r.Cmd, r.Args)
	if pkgs == nil {
		return []Runner{r}
	}
	rule := importFlagRules[0]
	if slices.ContainsFunc(r.Args, func(a string) bool { return a == rule.Name || strings.HasPrefix(a, rule.Name+"=") }) {
		return []Runner{r}
	}
	base := root
	if r.Dir != "" {
		base = r.Dir
	}
	var flagged, plain, dirs []string
	for _, p := range pkgs {
		if !strings.HasPrefix(p, "./") || strings.Contains(p, "...") {
			return []Runner{r}
		}
		dir := filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(p, "./")))
		if packageImports(dir, rule.Import) {
			flagged = append(flagged, p)
			dirs = append(dirs, dir)
		} else {
			plain = append(plain, p)
		}
	}
	if len(flagged) == 0 {
		return []Runner{r}
	}
	seeded := r
	seeded.Args = slices.Insert(with(flagged), 1, rule.Flag(contentSeed(base, dirs)))
	if len(plain) == 0 {
		return []Runner{seeded}
	}
	rest := r
	rest.Args = with(plain)
	return []Runner{seeded, rest}
}

// packageImports reports whether any Go file directly in dir names the quoted
// import path.
func packageImports(dir, quoted string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil && strings.Contains(string(b), quoted) {
			return true
		}
	}
	return false
}

// contentSeed is a nonzero seed derived from the module files and every file
// directly in the given package directories: equal content, equal seed.
func contentSeed(root string, dirs []string) uint64 {
	h := sha256.New()
	for _, name := range []string{"go.mod", "go.sum"} {
		b, _ := os.ReadFile(filepath.Join(root, name))
		fmt.Fprintf(h, "%s %d\n", name, len(b))
		h.Write(b)
	}
	sorted := slices.Clone(dirs)
	slices.Sort(sorted)
	for _, dir := range sorted {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			rel = dir
		}
		fmt.Fprintf(h, "dir %s\n", filepath.ToSlash(rel))
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			fmt.Fprintf(h, "file %s %d\n", e.Name(), len(b))
			h.Write(b)
		}
	}
	if seed := binary.BigEndian.Uint64(h.Sum(nil)[:8]); seed != 0 {
		return seed
	}
	return 1
}

// runRapidSplit is r run as splitRapidRuns cuts it, one after the other under
// one deadline; the first run that does not pass ends the run and is the
// verdict, the outputs joined so a rapid failure's seed line is kept.
func runRapidSplit(r Runner, root string, limit time.Duration, one SuiteRunner) SuiteResult {
	runs := splitRapidRuns(r, root)
	if len(runs) < 2 {
		return one(runs[0], root)
	}
	deadline := time.Now().Add(limit)
	if !r.Deadline.IsZero() && r.Deadline.Before(deadline) {
		deadline = r.Deadline
	}
	var merged SuiteResult
	for _, sub := range runs {
		sub.Deadline = deadline
		res := one(sub, root)
		joinResult(&merged, res)
		if !res.Passed || res.TimedOut {
			return merged
		}
	}
	return merged
}
