level: minor

One schema now describes the settings a repo and a user declare, and `aphrollo config` can show and change them.

### What you will notice

- `aphrollo config show` prints every setting with its effective value and the layer it came from: built-in, the user's `config.toml` (with a `[repo."<id>"]` section per repo), or the repo's `trellis.toml`. A key read from `aphrollo.toml` says which old key it is an alias of; when `trellis.toml` also declares it, `trellis.toml` wins and the row names the alias it overrides. `aphrollo.toml` keys with no equivalent (`go-test-reads`, `retro-on`, ...) are listed under their own names and keep working as before.
- A key or value a file gets wrong is named on stderr with its file, layer and line, and that layer's value is the built-in one, never off. `undercover = true # why` reads as true.
- `aphrollo config set <key> <value> [--user|--repo] [--dry]` writes one key to `trellis.toml` or the user's `config.toml` and records a `config.set` event holding the key, the value and the layer. `--dry` prints the line it would write.
- `aphrollo config` and `aphrollo config features` still print the opt-in feature table.
- The user's config directory is `$TRELLIS_CONFIG`, else `trellis` under the per-user local data directory on Windows or under `$XDG_CONFIG_HOME` (`~/.config`) elsewhere.
- New setting `tdd` (`enforce`, `warn`, `off`) grades the Stop and SubagentStop checks. The built-in default is `enforce`, today's behaviour: a stop with an unseen red is blocked. Under `warn` the stop is never blocked and shows one line of guidance, and the red stays for the next hook to report; under `off` the checks say nothing. `/tdd off` still wins over all three. The architecture's default is `warn` "until the A/B decides": phase A's A/B flips the built-in default, in one place (`internal/config`), when it does. TaskCompleted is not held open under `warn` or `off`.
- Every other setting is still read by its old reader; they move to the schema in a later change.
