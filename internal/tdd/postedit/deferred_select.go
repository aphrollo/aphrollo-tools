package postedit

// withSelect is r carrying the selection a stored job or queued run recorded.
func withSelect(r Runner, s *Selection) Runner {
	r.Select = s
	return r
}

// runnerFromJob is the runner a stored job runs, with the selection it was
// narrowed by, so a harvested verdict says which tests it ran.
func runnerFromJob(j DeferredJob) Runner {
	return withSelect(runnerFromArgv(j.Runner, j.Dir), j.Select)
}
