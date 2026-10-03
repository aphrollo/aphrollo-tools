# Changelog fragments

A PR that changes what a consumer of aphrollo sees adds exactly one file here,
`changelog.d/<lane>.md` (the lane or a short slug; letters, digits, `.`, `_`
and `-`, no directory). A PR never edits the VERSION file (it is gone) or the
sections of `CHANGELOG.md` (the record of the hand-written releases): the version
is made on main, from the fragments, as a release tag.

The file starts with its level, then the words a consumer reads, in the voice of
the 1.x changelog sections: a sentence saying what changed, then what you will
notice and what migrates by itself. Plain paragraphs and bullets; a `###`
heading is fine, a `#` or `##` heading is refused (the release owns those).

```
level: minor

A law now reads comments, so a banned word in one is found.

### What you will notice

- `module_size` counts a comment-only file as ...
```

The PR body says the same level on a line of its own, `version: patch`,
`version: minor` or `version: major`, and `aphrollo version check` (CI's
`version-check` job) refuses a PR whose fragment disagrees with it. A PR that
moves nothing a consumer sees says `version: none` and adds no fragment. A
change to a law preset, a language row or a mask is `minor` at least.

- `patch`: a fix that moves no verdict a consumer's gate gave before.
- `minor`: a new check, law, verb or flag, or a verdict that moves.
- `major`: a consumer must change something to keep working.

When a push to main brings fragments the newest `v*` tag does not hold,
`aphrollo release plan` names the tag they earn: the newest tag bumped by the
highest level among them. The release job makes the tag and a GitHub Release
whose notes are those fragments. A fragment stays in this directory after its
release, as history: `aphrollo changelog` prints every release's notes, newest
first, by finding the first tag whose tree holds each fragment. A merged
fragment is never edited or deleted.
