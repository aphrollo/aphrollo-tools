# Deploy — aphrollo dev-env CLI

`/usr/local/bin/aphrollo` deploys **on merge to `main`**, the same way the Go
services do, but what it ships is the newest release **tag** (`v<MAJOR.MINOR.PATCH>`),
not the tip of `main`: no PR carries a version, so the `release` job asks
`aphrollo release plan` (built from that very commit) which tag the merge
earns from the `changelog.d` fragments the newest tag does not yet contain,
tags it and creates a GitHub Release whose notes are those fragments
(`deploy/tag-release.sh`; a tag already present is `[skip]`, and no commit is
made to `main`). The binary learns its version from that tag: the build stamps
`-X .../internal/buildinfo.version=<tag>` (the `Build` step of
`.github/workflows/deploy.yml`, and `aphrollo update`, which builds a detached
worktree at the tag), and a test pins that the deploy build carries the stamp.
A build at no release tag reports `0.0.0-dev+<sha>`. The `deploy`
job of its own workflow (`.github/workflows/deploy.yml`)
checks out the newest tag (`deploy/newest-tag.sh`), builds the binary on the self-hosted runner and runs
`deploy/deploy-prod.sh`, which stages the build and hands it to the root-owned
installer (`aphrollo-install-release`): it verifies it, installs a release,
smoke-tests it, and atomically swaps a `current` symlink. No manual
`deploy-infra` step, no stale-operator-clone footgun.

The deploy is not a job of the pipeline run. It waits for the one self-hosted
runner, and a pipeline run held open by a down host queues every later push to
`main`, where GitHub cancels all but the newest, so those releases would never be
tagged. Instead the pipeline's `release` job, after tagging and only once test,
lint, scan and workflow-pins passed for the commit, runs
`gh workflow run deploy.yml --ref main` (a tag pushed with `GITHUB_TOKEN` starts no
workflow; a dispatch made with it does). `deploy.yml` has its own concurrency
group `deploy` with `cancel-in-progress: true`, so a newer release supersedes a
queued deploy, and a down host delays only the deploy, never a release tag.

## What a push to `main` re-runs

A push to `main` runs the pipeline again, but not the suites when the merged
pull request's own run already tested the same tree. The `changes` job builds
`tools/cireuse` and, on a push classified as code, asks GitHub for the pull
request whose merge commit is the pushed one, then for its `pipeline.yml` run
on the pull request's head. It answers `reuse=true` only when that run is
attempt 1, concluded success, ran the test step of `test`, every
`test-windows` shard, `gate-env` and `lint` to success, and published a
`tested-tree` artifact (the `changes` job uploads it on every pull request run)
equal to the pushed commit's tree. Then `test`, `test-windows`, `gate-env`,
`lint` and `benchmarks` are skipped, and `release` runs behind them as it runs
behind any skipped job. The job summary of `changes` names the pull request and
run whose verdict was reused, or says why not.

Anything else runs the full suite as before: no associated pull request, trunk
moved so the trees differ, a check not green, a re-run, a run from a fork or
another workflow, a lookup that failed, or a `cireuse` that did not build.
`scan` is not reused: `govulncheck` reads a vulnerability database that moves
without the tree. A docs-only or comment-only push is unchanged.

## Release layout

```
/opt/aphrollo-cli/
  releases/
    20260617-1a2b3c4/aphrollo   # one dir per deploy: <ts>-<sha7>
    ...
  current  ->  releases/<ts>-<sha7>     # atomically swapped by the installer
/usr/local/bin/aphrollo  ->  /opt/aphrollo-cli/current/aphrollo
```

Every coder/devops/operator session — and the TDD git gate / Bash guardrail
hook — execs `/usr/local/bin/aphrollo` fresh per call, so a swapped `current` is
picked up on the next exec. There is **no daemon to restart**. `/opt/aphrollo-cli`
is **root-owned**: every session runs this binary, so the runner never writes it.
The deploy stages its build in `/var/lib/aphrollo-release-staging/aphrollo-cli`
and runs `sudo aphrollo-install-release aphrollo-cli`, the one sudoers grant it
holds for this.

## Safety

The new binary is **smoke-tested before the swap** (`aphrollo tdd --help`,
`aphrollo --help`), so a non-runnable build never becomes `current` — the last
good release keeps serving. Rollback is therefore implicit; to force one, point
`current` back at a prior `releases/<…>` dir:

```
ln -sfn /opt/aphrollo-cli/releases/<prev> /opt/aphrollo-cli/current.new
mv -Tf  /opt/aphrollo-cli/current.new      /opt/aphrollo-cli/current
```

## Server prerequisites (one-time, provisioned by aphrollo-infra)

`aphrollo-infra` (its `site.yml` playbook) owns these — the app's deploy owns
only `current`:

- `/opt/aphrollo-cli` and `/opt/aphrollo-cli/releases` — `root`-owned, `0755`
  (only the installer writes releases and swaps `current` here).
- `/var/lib/aphrollo-release-staging/aphrollo-cli` — `github-runner`-owned
  staging dir the deploy copies its build into.
- `/usr/local/bin/aphrollo` — a **symlink** → `/opt/aphrollo-cli/current/aphrollo`
  (created by root once; `/usr/local/bin` is not runner-writable).
- A one-time bootstrap that seeds `current` from the operator clone **only if it
  is absent**, so `/usr/local/bin/aphrollo` resolves immediately after the infra
  apply and before the first on-merge deploy — and so routine infra applies never
  clobber a newer on-merge release.

Until the infra prereqs land, the deploy job fails fast at the
`$RELEASES missing` guard rather than writing anywhere unexpected.
