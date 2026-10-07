# Release — aphrollo dev-env CLI

Nothing deploys on merge. A merge to `main` is **tagged**, and every box moves
to a tag by running `aphrollo update` itself, so the binary we test is the one
we ship.

No PR carries a version, so the `release` job asks `aphrollo release plan`
(built from that very commit) which tag the merge earns from the `changelog.d`
fragments the newest tag does not yet contain, tags it and creates a GitHub
Release whose notes are those fragments (`deploy/tag-release.sh`; a tag already
present is `[skip]`, and no commit is made to `main`). `deploy/newest-tag.sh`
names the newest tag. The binary learns its version from that tag: `aphrollo
update` builds a detached worktree at the tag and stamps
`-X .../internal/buildinfo.version=<tag>`. A build at no release tag reports
`0.0.0-dev+<sha>`.

## Installing and switching versions

`aphrollo update` needs no root. It installs the newest tag into a versioned
directory under the account's own space and points `current` at it:

```
~/.aphrollo/bin/                          (Windows: %LOCALAPPDATA%\aphrollo\bin\)
  1.14.0/aphrollo
  1.15.0/aphrollo
  current                                 # a file holding the version the hooks run
```

- The newest three versions are kept; the one `current` names is always kept.
- `aphrollo update --to 1.14.0` switches back, with no fetch and no build.
- The pointer moves last, after the build and its smoke check, so a failed
  update changes nothing. A new version is a new directory, so a running binary
  is never replaced: Windows, which cannot replace one, installs the same way.
- Hooks (session hooks, git-hook shims, queue shims) run the user-space
  `current` first and the previously installed path second, and a hook whose
  binary is missing or over its budget is a no-op that exits 0 with one stderr
  line. Hooks never download; only `aphrollo update` does.
- The event log, gate state and caches live in the one version-independent
  state dir, never inside a version directory, so an update or a prune loses
  none of them. Every event records the version that wrote it (`binver`), and
  `stats`, `stats --ab`, `stats --shadow` and `report` take `--by-version`.
- `aphrollo version` says which binary is running and whether it is the
  user-space current.

`aphrollo update` and `gate init` also write a launcher, `~/.aphrollo/bin/aphrollo` (`aphrollo.cmd` on Windows), that runs the pointed binary like a hook does. Put that directory first on PATH so typing `aphrollo` follows updates; update prints one line when PATH resolves elsewhere and never edits PATH or rc files.

A root-owned `/usr/local/bin/aphrollo` from the old deploy keeps working as the
fallback and is never written by anything here.

## What a push to `main` re-runs

A push to `main` runs the pipeline again, but not the suites when the merged
pull request's own run already tested the same tree. The `changes` job runs
`aphrollo ci reuse` and, on a push classified as code, asks GitHub for the pull
request whose merge commit is the pushed one, then for its `pipeline.yml` run
on the pull request's head. It answers `reuse=true` only when that run is
attempt 1, concluded success, ran the test step of `test`, every
`test-windows` shard, `gate-env` and `lint` to success, and published a
`tested-tree` artifact (the `changes` job uploads it on every pull request run)
equal to the pushed commit's tree. Then `test`, `test-windows`, `gate-env`,
`lint` and `benchmarks` are skipped, and `release` runs behind them as it runs
behind any skipped job. The job summary of `changes` names the pull request and
run whose verdict was reused, or says why not.

The merge queue's group run decides the same way before the push does: when the
group holds the one pull request and its tree equals the tree that pull
request's run tested, `changes` answers `reuse=true` on the `merge_group` event
too, so the suites run once on the pull request and not again in the queue.
The windows shards still start on a merge group, with every step standing down,
because the queue requires the shard checks by name and a skipped matrix job does
not report them. A group of several pull requests always runs everything.

Anything else runs the full suite as before: no associated pull request, trunk
moved so the trees differ, a check not green, a re-run, a run from a fork or
another workflow, a lookup that failed, or an `aphrollo` that did not build.
`scan` is not reused: `govulncheck` reads a vulnerability database that moves
without the tree. A docs-only or comment-only push is unchanged.

