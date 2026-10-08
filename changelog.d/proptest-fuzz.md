level: minor

The gate now gives rapid a seed derived from the content of the packages that import it, so the same tree gets the same verdict and a new commit tries new inputs. A rapid failure still prints its own seed line, and CI and a plain `go test` stay random.

### What you will notice

- The tdd skill has a short Property tests section saying when a property test pays.
- The nightly fuzz workflow caps its workers, keeps the fuzz corpus between nights and uploads a crasher's reproducer as an artifact. A test now fails, naming the target, when a `func Fuzz` in the repo is missing from the workflow's target map; it found `FuzzLexer_AgreesWithTheLexerItReplaced` in `internal/mask`, which had never run nightly.

### Not shipped

A report-only law for a package that has a round-trip pair (Format and Parse, Marshal and Unmarshal, Encode and Decode) and no rapid or Fuzz test. The ratchet engine cannot say it as data: a law has one trigger and one excusing marker, it cannot require a second half, and it cannot match `Format<X>` with `Parse<X>` on the same name. A trigger on the writer half alone reported only false positives here (`FormatBytes` and the like). It needs a pair matcher in the engine first.
