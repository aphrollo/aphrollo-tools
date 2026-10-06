level: patch

An edit to a file the JS runner does not collect, such as a Playwright spec under e2e/, no longer reads red: vitest's and jest's "no test files found" exit is an empty run, like nextest's "no tests to run", so the edit hook and the Stop check no longer block on it.
