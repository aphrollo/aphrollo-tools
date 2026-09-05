package sqlc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/diff"
)

// ScopedFile is one generated file after the scoped merge: NewContent is the
// committed file with only the in-scope hunks applied; AppliedDiff shows those
// hunks (committed → new); DriftDiff shows the pre-existing drift that was left
// alone (new → clean regen).
type ScopedFile struct {
	Path        string
	NewContent  string
	AppliedDiff string
	DriftDiff   string
}

// ScopedResult is the outcome of RegenScoped for one config.
type ScopedResult struct {
	Config         Config
	Base           string
	ChangedQueries []string
	Files          []ScopedFile
}

// RegenScoped regenerates cfg into a temp dir, determines which queries changed
// in the working tree vs base (e.g. origin/main), and computes, per generated
// file, the committed content with ONLY the in-scope hunks applied. It does not
// write anything — call Apply for that.
func RegenScoped(cfg Config, base string) (*ScopedResult, error) {
	changed, err := changedQueries(cfg, base)
	if err != nil {
		return nil, err
	}
	set := inScopeSet(changed)
	inScope := func(name string) bool { return set[name] }

	regen, err := Regenerate(cfg)
	if err != nil {
		return nil, err
	}
	committed, err := committedFiles(cfg)
	if err != nil {
		return nil, err
	}

	res := &ScopedResult{Config: cfg, Base: base, ChangedQueries: changed}
	for _, path := range sortedKeys(committed, regen) {
		c, r := committed[path], regen[path]
		newContent := scopedMerge(c, r, inScope)
		res.Files = append(res.Files, ScopedFile{
			Path:        path,
			NewContent:  newContent,
			AppliedDiff: diff.Unified(path, c, newContent),
			DriftDiff:   diff.Unified(path, newContent, r),
		})
	}
	return res, nil
}

// Apply writes NewContent for every file whose in-scope hunks changed it.
// Files with no applied hunks (only drift, or clean) are left untouched.
func (r *ScopedResult) Apply() error {
	for _, f := range r.Files {
		if f.AppliedDiff == "" {
			continue
		}
		dst := filepath.Join(r.Config.Repo, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(f.NewContent), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.Path, err)
		}
	}
	return nil
}

// Render reports the changed queries, the in-scope hunks (applied or to-apply),
// and the pre-existing drift under a loud banner directing it to a separate PR.
func (r *ScopedResult) Render(apply bool) string {
	var b strings.Builder
	verb := "would apply"
	if apply {
		verb = "applied"
	}
	fmt.Fprintf(&b, "sqlc regen --scoped: %s (base %s)\n", r.Config.Name, r.Base)
	if len(r.ChangedQueries) == 0 {
		fmt.Fprintf(&b, "  no queries changed vs %s — nothing in scope to apply\n", r.Base)
	} else {
		fmt.Fprintf(&b, "  changed queries: %s\n", strings.Join(r.ChangedQueries, ", "))
	}

	var applied, drifted []ScopedFile
	for _, f := range r.Files {
		if f.AppliedDiff != "" {
			applied = append(applied, f)
		}
		if f.DriftDiff != "" {
			drifted = append(drifted, f)
		}
	}

	fmt.Fprintf(&b, "\nin-scope hunks (%s):\n", verb)
	if len(applied) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	}
	for _, f := range applied {
		fmt.Fprintf(&b, "\n  %s:\n", f.Path)
		fmt.Fprint(&b, indent(f.AppliedDiff, "    "))
	}

	if len(drifted) > 0 {
		fmt.Fprint(&b, "\n"+driftBanner+"\n")
		for _, f := range drifted {
			fmt.Fprintf(&b, "\n  %s:\n", f.Path)
			fmt.Fprint(&b, indent(f.DriftDiff, "    "))
		}
		fmt.Fprint(&b, "\n"+wholeSchemaNote+"\n")
	}

	if !apply {
		fmt.Fprint(&b, "\nrun again with --apply to write the in-scope hunks.\n")
	}
	return b.String()
}

// driftBanner heads the pre-existing-drift section. It is intentionally loud:
// the whole point of --scoped is that this drift is NOT yours to fix here.
const driftBanner = `━━━ PRE-EXISTING DRIFT — NOT APPLIED — fix in a SEPARATE PR ━━━
The hunks below already differed from a clean regen before your change. They were
left untouched so your diff stays scoped. Do NOT hand-apply them here.`

// changedQueries returns the sorted union of query names that changed in cfg's
// query files between base and the working tree.
func changedQueries(cfg Config, base string) ([]string, error) {
	files, err := queryFiles(cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var all []string
	for _, rel := range files {
		work, err := os.ReadFile(filepath.Join(cfg.Repo, rel))
		if err != nil {
			return nil, err
		}
		baseSrc, err := gitShow(cfg.Repo, base, rel)
		if err != nil {
			return nil, fmt.Errorf("changed queries for %s: %w", rel, err)
		}
		for _, n := range changedNamesBetween(baseSrc, string(work)) {
			if !seen[n] {
				seen[n] = true
				all = append(all, n)
			}
		}
	}
	sort.Strings(all)
	return all, nil
}

// queryFiles lists the .sql query files for every entry in cfg, as repo-relative
// paths. Each of an entry's `queries` paths (sqlc v2 allows either a single
// scalar or a list) may name a single file or a directory. An entry with no
// queries path is refused rather than silently falling back to a directory
// stat of "" (== the repo root), which would walk the entire repository for
// .sql files instead of the configured scope.
func queryFiles(cfg Config) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, e := range cfg.Entries {
		if len(e.Queries) == 0 {
			return nil, fmt.Errorf("sql entry for %s has no queries path", e.Out)
		}
		for _, q := range e.Queries {
			abs := filepath.Join(cfg.Repo, q)
			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("queries path %s: %w", q, err)
			}
			if !info.IsDir() {
				add(filepath.ToSlash(q))
				continue
			}
			err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
					return err
				}
				rel, err := filepath.Rel(cfg.Repo, path)
				if err != nil {
					return err
				}
				add(filepath.ToSlash(rel))
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
