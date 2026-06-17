# Deploy — aphrollo dev-env CLI

`/usr/local/bin/aphrollo` deploys **on merge to `main`**, the same way the Go
services do: the `deploy` job in `.github/workflows/pipeline.yml` builds the
binary on the self-hosted runner and runs `deploy/deploy-prod.sh`, which stages a
release, smoke-tests it, and atomically swaps a `current` symlink. No manual
`deploy-infra` step, no stale-operator-clone footgun.

## Release layout

```
/opt/aphrollo-cli/
  releases/
    20260617-1a2b3c4/aphrollo   # one dir per deploy: <ts>-<sha7>
    ...
  current  ->  releases/<ts>-<sha7>     # atomically swapped by deploy-prod.sh
/usr/local/bin/aphrollo  ->  /opt/aphrollo-cli/current/aphrollo
```

Every coder/devops/operator session — and the TDD git gate / Bash guardrail
hook — execs `/usr/local/bin/aphrollo` fresh per call, so a swapped `current` is
picked up on the next exec. There is **no daemon to restart**, hence the deploy
needs **no sudo**: it only writes under `/opt/aphrollo-cli` (runner-writable).

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

`aphrollo-infra` (`ansible/site.yml`) owns these — the app's deploy owns only
`current`:

- `/opt/aphrollo-cli` and `/opt/aphrollo-cli/releases` — `github-runner`-owned,
  `0755` (the runner stages releases + swaps `current` here).
- `/usr/local/bin/aphrollo` — a **symlink** → `/opt/aphrollo-cli/current/aphrollo`
  (created by root once; `/usr/local/bin` is not runner-writable).
- A one-time bootstrap that seeds `current` from the operator clone **only if it
  is absent**, so `/usr/local/bin/aphrollo` resolves immediately after the infra
  apply and before the first on-merge deploy — and so routine infra applies never
  clobber a newer on-merge release.

Until the infra prereqs land, the deploy job fails fast at the
`$RELEASES missing` guard rather than writing anywhere unexpected.
