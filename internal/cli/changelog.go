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

const changelogUsage = `usage: aphrollo changelog [--repo DIR] [--rev REV] [--tag vX.Y.Z]

Read-only. Prints the full changelog, newest first: the changelog.d fragments
at REV (default HEAD) that no release tag holds yet as an "Unreleased" block,
then for each release tag after the newest section of CHANGELOG.md the fragments its tag was the first to
contain, then CHANGELOG.md as the frozen record. Nothing is
committed: the file stays frozen and the releases live in changelog.d and in
GitHub Releases.

--tag prints only that release's notes, without a heading: the fragments the
tag was the first to contain (or, for a release CHANGELOG.md covers, its section of
CHANGELOG.md). That is what a GitHub Release carries. A fragment that cannot be
read is an error naming the file, exit 1.
`

func runChangelog(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, changelogUsage)
		return 0
	}
	fs := flag.NewFlagSet("changelog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "a directory inside the checkout to read")
	rev := fs.String("rev", "HEAD", "the commit whose changelog to assemble")
	tag := fs.String("tag", "", "print only this release tag's notes")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil || refuseArgs("changelog", pos, stderr) {
		return 2
	}
	g, err := openFragmentRepo(*repo)
	if err == nil {
		var text string
		if *tag != "" {
			text, err = tagNotes(g, *rev, *tag)
		} else {
			text, err = assembleChangelog(g, *rev)
		}
		if err == nil {
			fmt.Fprint(stdout, text)
			return 0
		}
	}
	fmt.Fprintf(stderr, "aphrollo changelog: %v\n", err)
	return 1
}

// attribution reads every later release tag's fragments and says which release
// first contained which, and what no tag holds yet.
func attribution(g fragmentRepo, rev string) (released []release.Release, unreleased []string, err error) {
	tags, err := g.tags()
	if err != nil {
		return nil, nil, err
	}
	changelog, err := fileAtRef(g.root, rev, compat.ChangelogFile)
	if err != nil {
		return nil, nil, err
	}
	frozen, _ := release.FrozenThrough(changelog)
	var trees []release.TagTree
	for _, r := range release.LaterReleaseTags(tags, frozen) {
		names, err := g.names(r.Tag)
		if err != nil {
			return nil, nil, err
		}
		trees = append(trees, release.TagTree{Tag: r.Tag, Version: r.Version, Fragments: names})
	}
	head, err := g.names(rev)
	if err != nil {
		return nil, nil, err
	}
	released, unreleased = release.Attribute(trees, head)
	return released, unreleased, nil
}

// readFragments parses the named fragments as rev holds them.
func readFragments(g fragmentRepo, rev string, names []string) ([]release.Fragment, error) {
	out := make([]release.Fragment, 0, len(names))
	for _, name := range names {
		f, err := g.fragment(rev, name)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func assembleChangelog(g fragmentRepo, rev string) (string, error) {
	frozen, err := fileAtRef(g.root, rev, compat.ChangelogFile)
	if err != nil {
		return "", err
	}
	released, unreleased, err := attribution(g, rev)
	if err != nil {
		return "", err
	}
	unreleasedFragments, err := readFragments(g, rev, unreleased)
	if err != nil {
		return "", err
	}
	var notes []release.ReleaseNotes
	for _, r := range slices.Backward(released) {
		fragments, err := readFragments(g, r.Tag, r.Fragments)
		if err != nil {
			return "", err
		}
		day, err := g.date(r.Tag)
		if err != nil {
			return "", err
		}
		notes = append(notes, release.ReleaseNotes{Heading: r.Version.String() + " - " + day, Fragments: fragments})
	}
	return release.Assemble(frozen, unreleasedFragments, notes), nil
}

// tagNotes is one release's notes: its first-contained fragments, or its
// section of the frozen changelog.
func tagNotes(g fragmentRepo, rev, tag string) (string, error) {
	tags, err := g.tags()
	if err != nil {
		return "", err
	}
	var version compat.Version
	known := false
	for _, t := range tags {
		if t != tag {
			continue
		}
		if name, isRelease := strings.CutPrefix(t, "v"); isRelease {
			if v, err := compat.ParseVersion(name); err == nil {
				version, known = v, true
			}
		}
	}
	if !known {
		return "", fmt.Errorf("%s is not a release tag (v<MAJOR.MINOR.PATCH>) of this repository", tag)
	}
	changelog, err := fileAtRef(g.root, rev, compat.ChangelogFile)
	if err != nil {
		return "", err
	}
	if frozen, _ := release.FrozenThrough(changelog); !frozen.Less(version) {
		body, ok := release.SectionBody(changelog, version)
		if !ok {
			return "", fmt.Errorf("%s has no section for %s", compat.ChangelogFile, version)
		}
		return body + "\n", nil
	}
	released, _, err := attribution(g, rev)
	if err != nil {
		return "", err
	}
	for _, r := range released {
		if r.Tag != tag {
			continue
		}
		fragments, err := readFragments(g, r.Tag, r.Fragments)
		if err != nil {
			return "", err
		}
		return release.Notes(fragments) + "\n", nil
	}
	return "", nil
}
