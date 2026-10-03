package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/release"
)

const releaseUsage = `usage: aphrollo release plan [--repo DIR] [--rev REV]

Read-only. Prints the release tag a push to main owes, or nothing when there is
none: the changelog.d fragments at REV (default HEAD) that the newest release
tag (v<MAJOR.MINOR.PATCH>) does not contain, bumped from that tag by the
highest level among them (patch, minor or major). With no new fragment, or a
tag already made for them, it prints nothing and exits 0, so a rerun of the
release job never makes a second tag. A fragment it cannot read, a newest tag
that is not behind REV, or no release tag at all is an error, exit 1. The tag
is on stdout alone; the level and the fragments it carries are on stderr. The
release workflow makes the tag and its GitHub Release from this answer, and
the Release's notes are aphrollo changelog --tag <tag>.
`

func runRelease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, releaseUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, releaseUsage)
		return 0
	case "plan":
		return runReleasePlan(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "aphrollo release: unknown subcommand %q\n\n%s", args[0], releaseUsage)
	return 2
}

func runReleasePlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "a directory inside the checkout to read")
	rev := fs.String("rev", "HEAD", "the commit the tag would go on")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil || refuseArgs("release plan", pos, stderr) {
		return 2
	}
	g, err := openFragmentRepo(*repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo release plan: %v\n", err)
		return 1
	}
	plan, ok, err := planRelease(g, *rev)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo release plan: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "release: nothing to tag: no changelog fragment of level patch, minor or major at %s beyond the newest release tag\n", *rev)
		return 0
	}
	var names []string
	for _, f := range plan.Fragments {
		names = append(names, f.Name)
	}
	fmt.Fprintf(stderr, "release: %s (%s): %s\n", plan.Tag, plan.Level, strings.Join(names, ", "))
	fmt.Fprintln(stdout, plan.Tag)
	return 0
}

// planRelease reads the tags and the fragments from git and asks the pure plan.
func planRelease(g fragmentRepo, rev string) (release.Plan, bool, error) {
	tags, err := g.tags()
	if err != nil {
		return release.Plan{}, false, err
	}
	newest, _, found := release.NewestRelease(tags)
	if !found {
		// PlanRelease owns this refusal's words.
		return release.PlanRelease(tags, nil, nil)
	}
	if _, err := gitStdoutIn(g.root, "merge-base", "--is-ancestor", newest, rev); err != nil {
		return release.Plan{}, false, fmt.Errorf("the newest release tag %s is not an ancestor of %s: tagging %s would skip or reorder releases: %w", newest, rev, rev, err)
	}
	inNewest, err := g.names(newest)
	if err != nil {
		return release.Plan{}, false, err
	}
	atRev, err := g.names(rev)
	if err != nil {
		return release.Plan{}, false, err
	}
	var pending []release.Fragment
	for _, name := range atRev {
		if slices.Contains(inNewest, name) {
			continue
		}
		f, err := g.fragment(rev, name)
		if err != nil {
			return release.Plan{}, false, err
		}
		pending = append(pending, f)
	}
	return release.PlanRelease(tags, inNewest, pending)
}

// fragmentRepo reads tags and fragments out of a checkout's git objects, so an
// answer depends on commits and never on what the working tree happens to hold.
type fragmentRepo struct{ root string }

func openFragmentRepo(dir string) (fragmentRepo, error) {
	root := compat.RepoRoot(dir)
	if root == "" {
		return fragmentRepo{}, fmt.Errorf("%s is not inside a git repository", dir)
	}
	return fragmentRepo{root: root}, nil
}

// tags are the repository's v* tags.
func (g fragmentRepo) tags() ([]string, error) {
	out, err := gitStdoutIn(g.root, "tag", "--list", "v*")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// names are the names of the fragments in rev's tree, sorted.
func (g fragmentRepo) names(rev string) ([]string, error) {
	out, err := gitStdoutIn(g.root, "ls-tree", "-r", "--name-only", "-z", rev, "--", release.FragmentDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, file := range strings.FieldsFunc(out, func(r rune) bool { return r == 0 }) {
		if name, ok := release.FragmentName(file); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// fragment reads and parses the fragment name as rev holds it.
func (g fragmentRepo) fragment(rev, name string) (release.Fragment, error) {
	text, err := gitStdoutIn(g.root, "show", rev+":"+release.FragmentPath(name))
	if err != nil {
		return release.Fragment{}, err
	}
	return release.ParseFragment(name, text)
}

// date is the day a tag's commit was made, YYYY-MM-DD.
func (g fragmentRepo) date(tag string) (string, error) {
	out, err := gitStdoutIn(g.root, "log", "-1", "--format=%cs", tag)
	return strings.TrimSpace(out), err
}
