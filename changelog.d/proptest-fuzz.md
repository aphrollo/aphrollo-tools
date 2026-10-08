level: minor

The tdd skill gains a short Property tests section: when a property test pays, and what to do with a rapid red. A rapid failure prints its seed, a gate red from rapid is a real bug and not a flake, and you reproduce it with `-rapid.seed=<n>` on the same test.

### What you will notice

- The nightly fuzz workflow caps its workers, keeps the fuzz corpus between nights (saved even on a night with a crash) and uploads only the reproducer files that run added, not the checked-in seeds. A test now fails, naming the target, when a `func Fuzz` in the repo is missing from the workflow's target map; it found `FuzzLexer_AgreesWithTheLexerItReplaced` in `internal/mask`, which had never run nightly.

### Not shipped

- A gate-run rapid seed derived from the tree. It stops go caching rapid packages, splits the run so go loses cross-package parallelism and builds twice, and nondeterminism from rapid has never flipped a verdict here.
- A report-only law for a package with a round-trip pair (Format and Parse, Marshal and Unmarshal, Encode and Decode) and no rapid or Fuzz test. The ratchet engine cannot say it as data: a law has one trigger and one excusing marker, it cannot require a second half, and it cannot match `Format<X>` with `Parse<X>` on the same name. A trigger on the writer half alone reported only false positives here. It needs a pair matcher in the engine first.
