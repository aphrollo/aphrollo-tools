level: patch

The nightly fuzz job no longer stops before Go runs on a fresh checkout, and a test no longer fails when the mutation run turns Go's test cache off.

### What you will notice

- The nightly fuzz step treats a target with no checked-in corpus directory as an empty corpus.
- The test-cache end-to-end tests set their own Go flags, so the nightly mutants run passes them.
