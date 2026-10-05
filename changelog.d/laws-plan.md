level: minor

The laws are now planned in one place, and the edit-time smells are matcher kinds of the law engine.

### What you will notice

- A plan names which laws judge which files at the edit, commit and merge stages, and loads the laws once per process. The pre-edit judge, the post-edit judge and the commit gate read their law list from it, so a commit refusal by a deny law at a file you edited is named at the edit. The one exception is a module-graph law on a write whose content the agent harness did not hand over: the commit judges it.
- Repeated law loads inside one hook cost one read of the law files, not one per judge.
- New matcher kind `oracle-smell` with a `detector` key: `test-sleep`, `tautology`, `focused-test`, `disabled-test`, `error-kind-blind`, `panic-only-oracle`, `lint-suppress`, `type-suppress` and `coverage-suppress`. A detector reads the file as its own language lexes it, so a law needs neither `code_only` nor `mask_strings`, and the law's `escape` admits a hit by the usual rule (a reason after the token).
- New preset group `smells` (`aphrollo ratchet init --preset smells`) holds one law per detector. The four test-oracle smells that block at edit time are `deny`; the rest are `warn`, as at the edit gate. Existing hits are grandfathered by `ratchet check --adopt`, never by a hand-edited baseline.
- The edit gate's block and warn lines, its gate-log tokens and its escape comments (`real-time:`, `skip-ok:`, `any-error-ok:`, `panic-only-ok:`) are unchanged: the gate and the laws run one detector.
- `aphrollo stats` now reports "commit refusals the edit check missed": of the (law, file) pairs the commit gate refused on a law, how many no edit-time deny or guide event of the same lane had named earlier, over the stats window. A commit refusal event now records up to ten refused law and file names and their count (names only, never contents), and the post-edit and pre-edit judges record a `guide` event per law and file they name without denying. Refusals logged before this change record no pair and are listed as unattributed.
- Known gap, counted as missed: the dependency-graph laws are not judged after a write through the shell (a heredoc, `sed -i`, a script) to go.mod or Cargo.toml, because the post-edit judge has no proposed content to hand the graph and the query is too slow to run per edit. The commit judges them.
