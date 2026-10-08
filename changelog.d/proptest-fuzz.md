level: minor

The gate now gives rapid a seed derived from the content of the packages that import it, so the same tree gets the same verdict and a new commit tries new inputs. A rapid failure still prints its own seed line, and CI and a plain `go test` stay random.

### What you will notice

- A new info-level law, `roundtrip_untested`, reports a Go package with a Format, Marshal or Encode function and neither a rapid test nor a Fuzz target. It never blocks.
- The tdd skill has a short Property tests section saying when a property test pays.
- The nightly fuzz workflow caps its workers, keeps the fuzz corpus between nights and uploads a crasher's reproducer as an artifact.
