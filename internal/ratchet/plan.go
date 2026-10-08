package ratchet

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// One planner. Which laws apply to which files, at which stage, was answered
// three times: by the pre-edit judge, by the post-edit judge and by the commit
// gate, each loading the laws on its own and each deciding by its own loop what
// to run. A commit refusal the edit stage had not looked for (issue #968) was a
// disagreement between those loops. Plan is the one answer; the gates read
// their law list from it and a property test holds the edit stage to the
// commit's.

// Stage is when a plan is being made.
type Stage int

const (
	// StageEdit is a write that is about to happen or just happened: the laws
	// that cover the edited files. Fail-open: a gate that cannot plan lets the
	// edit through, the commit judges again.
	StageEdit Stage = iota
	// StageCommit is what a commit contains: every law, over the whole tree.
	// Fail-closed: a gate that cannot plan refuses.
	StageCommit
	// StageMerge is a merge's tree: planned as the commit, the tree being whole.
	StageMerge
)

func (s Stage) String() string {
	switch s {
	case StageEdit:
		return "edit"
	case StageCommit:
		return "commit"
	case StageMerge:
		return "merge"
	}
	return "unknown"
}

// Plan is a question to the planner: what to judge, at which stage.
type Plan struct {
	// Root is the repository whose laws are planned.
	Root  string
	Stage Stage
	// Base is the ref a change-judging law reads its pre-image from ("HEAD" at
	// the edit and commit stages); the planner passes it on, it never reads it.
	Base string
	// Files are the repository-relative slash paths in play: the edited ones at
	// the edit stage, the staged ones at the commit and merge stages.
	Files []string
	// Overlay is proposed content for files that are not on disk yet (the
	// pre-edit judge); nil when the files are as they sit.
	Overlay map[string]string
}

// Group is how a law is judged for an edit: the narrowest run that still
// answers what the commit would.
type Group int

const (
	// GroupPerFile laws judge one file by itself: a scan of the edited files.
	GroupPerFile Group = iota
	// GroupRegistry laws judge a registry both ways.
	GroupRegistry
	// GroupWholeTree laws span files (identifier, containment, ceiling): run
	// over their own scope.
	GroupWholeTree
	// GroupRemoved laws judge what a change took out, against a base.
	GroupRemoved
	// GroupChanged laws judge the set of files that differ from the base.
	GroupChanged
	// GroupGraph laws read the module graph, not a file.
	GroupGraph
)

// GroupOf places a matcher kind in its group.
func GroupOf(kind MatcherKind) Group {
	switch kind {
	case KindDepGraphForbids, KindDepGraphCeiling, KindGoDepGraphForbids:
		return GroupGraph
	case KindRegistryBothWays:
		return GroupRegistry
	case KindIdentResolves, KindFileSetContainment, KindJSONNumberCeiling, KindGoBenchCeiling, KindTestCost:
		return GroupWholeTree
	case KindSymbolRemoved:
		return GroupRemoved
	case KindCoChange, KindHunkRegex:
		return GroupChanged
	}
	return GroupPerFile
}

// CommitOnly reports whether the edit stage leaves a law to the commit. These
// are the commit-only laws, and the list is the module-graph laws judged
// without proposed content: a `go list` or `cargo metadata` costs hundreds of
// milliseconds, so a post-edit judgement (the files already on disk, no overlay
// to hand the graph) does not run them, while the pre-edit judgement, which
// carries the content, does. Every other law the commit refuses at an edited
// file is in the edit plan; the property test holds the planner to that.
func CommitOnly(law Law, overlay bool) bool {
	return !overlay && GroupOf(law.Matcher.Kind) == GroupGraph
}

// Step is one law's part of a plan.
type Step struct {
	Law   Law
	Group Group
	// Files are the plan's files that fall in the law's scope, sorted.
	Files []string
	// Whole is set when the law is judged over every file in its scope and not
	// only Files: every law at the commit and merge stages.
	Whole bool
}

// Steps is a plan's laws, in law-name order.
type Steps []Step

// Covers reports whether the plan judges the named law at the file.
func (ss Steps) Covers(law, rel string) bool {
	for _, s := range ss {
		if s.Law.Name == law && s.Law.Scope.Matches(rel) && (s.Whole || slices.Contains(s.Files, rel)) {
			return true
		}
	}
	return false
}

// Steps loads the repository's laws, once per process per content, and plans
// them. The error is the load's: the edit stage fails open on it, the commit
// stage closed.
func (p Plan) Steps() (Steps, error) {
	laws, err := LoadLaws(p.Root)
	if err != nil {
		return nil, err
	}
	return PlanOf(laws, p), nil
}

// PlanOf plans laws already loaded. A law this binary has no matcher for
// (UnknownKind) is never planned: Check reports it as skipped.
func PlanOf(laws []Law, p Plan) Steps {
	var out Steps
	for _, law := range laws {
		if law.UnknownKind != "" {
			continue
		}
		var in []string
		for _, rel := range p.Files {
			if law.Scope.Matches(rel) {
				in = append(in, rel)
			}
		}
		sort.Strings(in)
		in = slices.Compact(in)
		step := Step{Law: law, Group: GroupOf(law.Matcher.Kind), Files: in}
		if p.Stage == StageEdit {
			if len(in) == 0 || CommitOnly(law, p.Overlay != nil) {
				continue
			}
		} else {
			step.Whole = true
		}
		out = append(out, step)
	}
	return out
}

// lawParses counts the loads that read the laws from disk rather than from the
// process's cache.
var lawParses atomic.Int64

var lawCache struct {
	sync.Mutex
	byRoot map[string]cachedLaws
}

type cachedLaws struct {
	hash string
	laws []Law
}

// LoadLaws reads every law under <root>/.ratchet/laws, sorted by name, once per
// process for as long as what the load reads is unchanged: the law files, the
// scope sets and the language rows. A repo with no laws dir loads zero laws and
// no error — the engine is opt-in. A caller gets its own copy of the list, so
// what Check marks on a law is not seen by the next caller.
func LoadLaws(root string) ([]Law, error) {
	hash, ok := lawInputsHash(root)
	if !ok {
		return loadLawsCounted(root)
	}
	lawCache.Lock()
	cached, hit := lawCache.byRoot[root]
	lawCache.Unlock()
	if hit && cached.hash == hash {
		return slices.Clone(cached.laws), nil
	}
	laws, err := loadLawsCounted(root)
	if err != nil {
		return nil, err
	}
	lawCache.Lock()
	if lawCache.byRoot == nil {
		lawCache.byRoot = map[string]cachedLaws{}
	}
	lawCache.byRoot[root] = cachedLaws{hash: hash, laws: slices.Clone(laws)}
	lawCache.Unlock()
	return laws, nil
}

func loadLawsCounted(root string) ([]Law, error) {
	lawParses.Add(1)
	return loadLawsUncached(root)
}

// lawInputsHash is the digest of every file LoadLaws reads under root, by name
// and content. False when one cannot be read, which is a load that is not
// cached and reports its own error.
func lawInputsHash(root string) (string, bool) {
	h := sha256.New()
	dirs := []struct{ dir, suffix string }{
		{filepath.Join(root, filepath.FromSlash(LawsDir)), ".toml"},
		{filepath.Join(root, filepath.FromSlash(lang.Dir)), ".toml"},
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d.dir)
		if err != nil && !os.IsNotExist(err) {
			return "", false
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), d.suffix) {
				continue
			}
			if !hashFile(h, filepath.Join(d.dir, e.Name())) {
				return "", false
			}
		}
	}
	if !hashFile(h, filepath.Join(root, filepath.FromSlash(ScopesFile))) {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// hashFile writes a file's name and content into h; a file that is not there
// is part of the digest as absent, any other read failure is false.
func hashFile(h interface{ Write([]byte) (int, error) }, path string) bool {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	_, _ = h.Write([]byte{0})
	return true
}

// Options is the run a plan stands for. The edit stage judges its files alone
// and against themselves, so it narrows the scan to them and names no base; the
// commit and merge stages judge the whole tree from the base, with the files as
// the staged set a diff-scoped law reads.
func (p Plan) Options() Options {
	o := Options{Root: p.Root, Proposed: p.Overlay}
	if p.Stage == StageEdit {
		o.Files = p.Files
		return o
	}
	o.Base, o.StagedFiles = p.Base, p.Files
	return o
}
