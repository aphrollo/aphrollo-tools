package tdd

import "github.com/aphrollo/aphrollo-tools/internal/tdd/suite"

// CargoBuildOnlyArgv is suite.CargoBuildOnlyArgv for the queue shim: the
// compile-only form of a test run, shared with the edit hook's build phase.
func CargoBuildOnlyArgv(argv []string) []string { return suite.CargoBuildOnlyArgv(argv) }
