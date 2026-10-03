level: minor

A version now comes from the release tag instead of a file every PR had to bump, so two PRs open at once no longer collide on it.

### What you will notice

- `aphrollo version` prints the version of the release tag the binary was built at. A build that is not at a release tag, such as a `go build` by hand, prints `0.0.0-dev+<sha>`. `aphrollo update` and the deploy stamp the tag they build.
- A repo whose `aphrollo.toml` says `requires = ">=1.4"` is not judged against a dev build: the binary judges the repo as usual and prints one line saying the minimum went unchecked. A release build compares as before.
- `aphrollo release plan` prints the release tag a merge to main earns, and `aphrollo changelog` prints the whole history, assembled from the `changelog.d` fragments each release tag first contains, with `CHANGELOG.md` as the record of the releases written by hand. Releases after those are listed on the repo's GitHub Releases page.
- A PR in this repo says `version: none|patch|minor|major` as before and, unless it is `none`, adds one `changelog.d/<lane>.md` fragment instead of bumping the VERSION file and writing a `CHANGELOG.md` section. `aphrollo version check` holds the PR to that.

### What migrates by itself

- Nothing in a consuming repo changes: `requires` is compared with the release version exactly as before.
