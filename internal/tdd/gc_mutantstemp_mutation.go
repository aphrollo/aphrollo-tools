package tdd

import (
	"time"
)

// mutantsCopyActiveWindow is how recently a copy must have been touched to be
// attributed to a live run. A mutation run writes into its copy constantly
// (every mutant is an edit and a build), so a copy nothing has touched in half
// an hour is not one anybody is testing.
const mutantsCopyActiveWindow = 30 * time.Minute
