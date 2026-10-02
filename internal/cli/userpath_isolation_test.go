package cli

// No test in this package may read or write the operator's real
// HKCU\Environment: the default store is swapped for none before any test
// runs, and a test that wants a store installs a fake with useFakeUserPath.
func init() { userPathStoreFn = func() userPathStore { return nil } }
