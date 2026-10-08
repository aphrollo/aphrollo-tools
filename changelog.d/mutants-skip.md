level: minor

Calls that cannot fail no longer produce mutants that survive by construction, and the gate log records how long a mutation measurement took.

### What you will notice

- `mutants-skip` in `aphrollo.toml` lists calls as `<import path>.<Func>` whose error check no test can drive; `crypto/rand.Read` is on the list for every repo and your entries are added to it. Only the `err != nil` (or `err == nil`) comparison on the error such a call returns is skipped: other operators of the condition, anything in a function literal, and a local name that happens to match the import stay measured.
- The commit gate does not run the skipped mutants and says how many it skipped. CI reports them as `skipped` in its summary line, never as caught or missed, and a malformed entry is refused.
- The gate log's line for a finished mutation measurement now carries the seconds it took instead of 0, so the report can show what mutation spends.
