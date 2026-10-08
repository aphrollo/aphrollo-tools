level: minor

A JS repo can have the merge gate run its whole vitest suite, and the commit gate says plainly when it left files to the merge gate.

### What you will notice

- `premerge-js = "full"` under `[aphrollo]` makes the merge gate run `vitest run` instead of `vitest related <files>`, so a repo that wants the full suite as merge evidence no longer runs it twice. The default stays `"related"`; any other value refuses the merge and names the two choices.
- For an npm root with vitest or jest, the commit gate's mechanical line reads `NOT RUN — not tested at commit — the merge gate tests it: <files>; this pass is not a green for them`. A long list is a count and one example.
