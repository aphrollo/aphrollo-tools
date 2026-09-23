package tdd

// goRanNoTests reports whether a passing Go run executed no test in any
// package (goRunRanATest). A run whose packages cannot be read is not read
// as empty.
func goRanNoTests(r Runner, res SuiteResult) bool {
	if r.Cmd != "go" || res.TimedOut || !res.Passed {
		return false
	}
	ran, known := goRunRanATest(res)
	return known && !ran
}
