level: patch

The test suite is tiered, and five load-sensitive tests no longer depend on the clock.

### What you will notice

- Nothing in the tool's behaviour changes.
- Tests that start real language servers or wait out ten-second process-tree timeouts sit behind the `proc` build tag and run nightly on Linux and Windows (`nightly-proc.yml`); `go test -tags proc ./...` runs them by hand.
- Test packages that built a committed repo per test copy one built once at start-up.
