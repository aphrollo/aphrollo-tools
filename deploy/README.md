# Deploy — aphrollo dev-env CLI

`/usr/local/bin/aphrollo` deploys **on merge to `main`**, the same way the Go
services do, but what it ships is the newest release **tag** (`v<VERSION>`),
not the tip of `main`: the `release` job tags a merge that bumps
`internal/buildinfo/VERSION` (`deploy/tag-release.sh`; a tag already present is
`[skip]`), and the `deploy` job checks out the newest tag
(`deploy/newest-tag.sh`), builds the binary on the self-hosted runner and runs
`deploy/deploy-prod.sh`, which stages the build and hands it to the root-owned
installer (`aphrollo-install-release`): it verifies it, installs a release,
smoke-tests it, and atomically swaps a `current` symlink. No manual
`deploy-infra` step, no stale-operator-clone footgun.

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
