package lawgate

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// The post-edit half of the law engine. The pre-edit judge (ratchetgate.go)
// sees an Edit or Write before it lands, and three kinds of commit refusal
// got past it (issue #968):
//
//   - a law that judges a CHANGE rather than a file. symbol-removed compares
//     the tree with a base, and the pre-edit judge passes none, so a removed
//     test was never judged before the commit.
//   - a registry-both-ways law's unused-entry half. A scan narrowed to the
//     edited file reads the registry but never the files that use it, so a
//     README example naming no verb was only judged over the whole tree.
//   - a write through the shell. A heredoc, a `sed -i` or a script fires no
//     pre-edit hook at all, and its content does not exist before it runs;
//     the post-edit judge is the first to see it, and names the hit right
//     after the write instead of at `git commit`.
//
// So after the write, the edited files are judged as they sit on disk by the
// laws whose scope covers them, and each would-be refusal is named with its
// escape. Nothing is blocked: the write has happened, and the commit gate
// still judges the whole staged tree.
//
// Each law runs alone (Options.Only), in the narrowest form that still
// answers what the commit gate would, and not at all when this edit cannot
// have changed its answer (editJudge.plan):
//
//   - a per-file law over the edited files only;
//   - a registry law over the edited files, which answers its "used but not
//     registered" half; over its whole scope only when the edit touched the
//     registry file or took a use out of an edited file, the two ways an
//     entry can lose its last use;
//   - any other law whose answer spans files (identifier, containment,
//     ceiling) over its own scope;
//   - symbol-removed only when a name the pattern captured in an edited file
//     at HEAD is gone from it now; then over the whole tip, against a base
//     holding only the edited files at HEAD — the name may stand elsewhere;
//   - co-change and hunk-regex over the files that differ from HEAD, the set
//     a commit made now would stage, reporting only what lands in an edited
//     file.
//
// The dependency-graph laws are left to the pre-edit and commit gates: they
// read the module graph, not a file, and cost hundreds of milliseconds each.

// editJudge is one post-edit judging: the edited files, their content on
// disk, and — each read once, only when a law asks — the edited files at
// HEAD and every file a commit made now could stage, at HEAD. A law that
// needs neither costs no git process.
type editJudge struct {
	root   string
	rels   []string
	edited map[string]bool
	now    map[string]string
	// head is filled by headOf, changed and changedHead by changedSet.
	head        headFiles
	changed     []string
	changedHead headFiles
}

// newEditJudge reads the edited files from disk. A file missing there reads
// as empty.
func newEditJudge(root string, rels []string) *editJudge {
	j := &editJudge{root: root, rels: rels, edited: map[string]bool{}, now: map[string]string{}}
	for _, rel := range rels {
		j.edited[rel] = true
		j.now[rel] = onDiskContent(filepath.Join(root, filepath.FromSlash(rel)))
	}
	return j
}

// headOf is the edited files at HEAD, read in one batch on first use. A file
// HEAD does not carry is absent.
func (j *editJudge) headOf() headFiles {
	if j.head == nil {
		j.head = headFiles(headBlobs(j.root, j.rels))
	}
	return j.head
}

// editLawRefusals names, one rendered line per finding, what the commit gate
// would refuse in the files rels (repo-relative, slash-separated) as they sit
// on disk under root. Only a deny law refuses; a warn law is no refusal. A
// law that cannot be judged here (unreadable, a git failure) says nothing:
// the commit gate judges it again.
func editLawRefusals(root string, rels []string) []string {
	if root == "" || len(rels) == 0 || !ratchet.HasLaws(root) {
		return nil
	}
	// The edit stage fails open: a plan that cannot be made says nothing here and
	// the commit, which fails closed on the same error, judges.
	steps, err := ratchet.Plan{Root: root, Stage: ratchet.StageEdit, Base: "HEAD", Files: rels}.Steps()
	if err != nil {
		return nil
	}
	j := newEditJudge(root, rels)
	// The per-file laws share one narrowed scan; every other law runs alone.
	batch := ratchet.Options{Root: root, Files: rels}
	runs := []ratchet.Options{}
	onlyEdited := map[string]bool{}
	for _, step := range steps {
		law := step.Law
		if law.Severity != ratchet.Deny {
			continue
		}
		opts, run := j.plan(law)
		if !run {
			continue
		}
		onlyEdited[law.Name] = ratchet.GroupOf(law.Matcher.Kind) == ratchet.GroupChanged
		if len(opts.Laws) != 0 {
			batch.Laws = append(batch.Laws, opts.Laws...)
			continue
		}
		runs = append(runs, opts)
	}
	if len(batch.Laws) != 0 {
		runs = append(runs, batch)
	}
	var findings []ratchet.Finding
	for _, opts := range runs {
		res, err := ratchet.Check(opts)
		if err != nil {
			continue
		}
		for _, f := range res.Findings {
			if f.Severity == ratchet.Deny.String() && (!onlyEdited[f.Law] || j.edited[f.File]) {
				findings = append(findings, f)
			}
		}
	}
	return ratchet.Result{Findings: findings}.Lines()
}

// plan is the Check a law gets for this edit, and false when this edit
// cannot have changed its answer. A plan naming the law in Laws rather than
// Only is a scan of the edited files alone, which editLawRefusals runs once
// for every such law together.
func (j *editJudge) plan(law ratchet.Law) (ratchet.Options, bool) {
	narrowed := ratchet.Options{Root: j.root, Files: j.rels, Laws: []string{law.Name}}
	opts := ratchet.Options{Root: j.root, Only: law.Name}
	switch ratchet.GroupOf(law.Matcher.Kind) {
	case ratchet.GroupGraph:
		return opts, false
	case ratchet.GroupPerFile:
		return narrowed, true
	case ratchet.GroupRegistry:
		if !j.registryTouched(law) {
			return narrowed, true
		}
	case ratchet.GroupRemoved:
		if !j.anyCaptureDropped(law) {
			return opts, false
		}
		opts.Base, opts.BaseTree = "HEAD", j.headOf()
	case ratchet.GroupChanged:
		changed, head := j.changedSet()
		opts.Files, opts.StagedFiles, opts.Base, opts.BaseTree = changed, changed, "HEAD", head
	}
	return opts, true
}

// registryTouched reports whether this edit could have left a registry entry
// with no use: it edited the registry file, or a use left an edited file.
func (j *editJudge) registryTouched(law ratchet.Law) bool {
	return j.edited[law.Matcher.RegistryFile] || j.capturesDropped(law, law.Matcher.UsePattern)
}

// anyCaptureDropped is capturesDropped over every pattern a symbol-removed law
// captures with: its own, or its scope's language rows' test patterns. A law
// whose patterns cannot be resolved is judged in full, where the engine names
// the defect.
func (j *editJudge) anyCaptureDropped(law ratchet.Law) bool {
	patterns, err := law.SymbolPatterns()
	if err != nil {
		return true
	}
	for _, re := range patterns {
		if j.capturesDropped(law, re) {
			return true
		}
	}
	return false
}

// capturesDropped reports whether some name re captured in an in-scope
// edited file at HEAD is no longer captured in that file on disk.
func (j *editJudge) capturesDropped(law ratchet.Law, re *regexp.Regexp) bool {
	whole := wholeTextPattern(re)
	for _, rel := range j.rels {
		if !law.Scope.Matches(rel) {
			continue
		}
		now := captures(whole, j.now[rel])
		for name := range captures(whole, j.headOf()[rel]) {
			if !now[name] {
				return true
			}
		}
	}
	return false
}

// changedSet is every path a commit made now could stage, with those paths
// at HEAD, read once.
func (j *editJudge) changedSet() ([]string, headFiles) {
	if j.changed == nil {
		j.changed = changedFromHead(j.root, j.rels)
		j.changedHead = headFiles(headBlobs(j.root, j.changed))
	}
	return j.changed, j.changedHead
}

// wholeTextPattern matches re against a whole file's text with ^ and $
// binding to each line, as the engine matches a symbol-removed pattern.
func wholeTextPattern(re *regexp.Regexp) *regexp.Regexp {
	return regexp.MustCompile("(?m)" + re.String())
}

// captures is the set of names re captures in text: per match, the last
// group that captured anything, the rule the registry matcher reads a
// use-pattern alternation by.
func captures(re *regexp.Regexp, text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		name := ""
		for _, g := range m[1:] {
			if g != "" {
				name = g
			}
		}
		out[name] = true
	}
	return out
}

// changedFromHead is every path a commit made now could stage: the tracked
// files that differ from HEAD and the untracked ones git does not ignore,
// plus rels themselves, sorted. One `git status` answers both halves.
func changedFromHead(root string, rels []string) []string {
	seen := map[string]bool{}
	for _, rel := range rels {
		seen[rel] = true
	}
	// The hook's one status answers it; a rename is both its paths, as with
	// renames off. A failed read (an unreadable index) leaves the edited files
	// as the whole set.
	if _, st := gitx.HookStatus(root); st != nil {
		for _, e := range st.Entries {
			if e.Kind != igit.Ignored {
				seen[e.Path] = true
				if e.From != "" {
					seen[e.From] = true
				}
			}
		}
	} else if status, err := git(root, "status", "--porcelain", "-uall", "-z", "--no-renames"); err == nil {
		for _, entry := range strings.Split(status, "\x00") {
			if m := porcelainEntryRe.FindStringSubmatch(entry); m != nil {
				seen[m[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// porcelainEntryRe reads one `git status --porcelain -z --no-renames` entry:
// two status letters and a space ahead of the path.
var porcelainEntryRe = regexp.MustCompile(`(?s)^.. (.+)$`)

// headFiles is a base tree holding only some files at HEAD: what a
// symbol-removed law compares the tip against when only those files changed.
type headFiles map[string]string

func (h headFiles) List() ([]string, error) {
	out := make([]string, 0, len(h))
	for rel := range h {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

func (h headFiles) Read(path string) ([]byte, error) {
	return []byte(h[path]), nil
}

func (h headFiles) ReadAll(paths []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, p := range paths {
		if text, ok := h[p]; ok {
			out[p] = []byte(text)
		}
	}
	return out, nil
}

// headBlobs is each of rels (repo-relative) as HEAD holds it, a path HEAD does
// not carry absent. The blobs the hook's one status names are read from the
// object store without a spawn; the paths only git can place (clean, ignored,
// in conflict) go to git in one batch.
func headBlobs(root string, rels []string) map[string]string {
	out := make(map[string]string, len(rels))
	var asked []string
	for _, rel := range rels {
		text, inHead, ok := gitx.HeadCopy(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case !ok:
			asked = append(asked, rel)
		case inHead:
			out[rel] = text
		}
	}
	for rel, text := range gitBatchBlobs(root, "HEAD", asked) {
		out[rel] = text
	}
	return out
}
