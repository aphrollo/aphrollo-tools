level: patch

A Go repo can let go's own test cache serve the packages a change left alone, in the post-edit suite and the commit's suite, with `test-cache = "edit"` or `"commit"` under `[aphrollo]`. It is off by default.

### What you will notice

- Nothing changes until a repo sets the key. With it off, every `go test` the gate runs carries `-count=1` as before, byte for byte.
- With it on, the post-edit suite (and at `"commit"` the commit's suite) drops `-count=1` and `-shuffle=on`, since go never serves a run that shuffles from its cache. The merge and CI always run the whole tree with `-count=1 -shuffle=on` and no cache, and mutation keeps its own `-count=1`.
- `test-cache-impure = ["./internal/git/..."]` names the packages whose tests go cannot vouch for, such as ones that run git or another binary. Those packages run apart with `-count=1` every time, even with the cache on. A run of `./...` cannot be split, so it runs uncached.
- A run that was served from the cache says so: `green (12 passed (3 cached), 1.2s)`. The `(cached)` lines stay in the retained output, and the gate event carries `cached_packages`.
