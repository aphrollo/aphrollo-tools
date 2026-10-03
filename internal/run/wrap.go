package run

import "github.com/aphrollo/aphrollo-tools/internal/argvbatch"

// These forward to internal/argvbatch, whose inventory tests name the call
// sites it bounds. Those sites move behind run package by package, and the
// code goes with the last of them.

// Budget is the longest command line a caller builds, in characters.
const Budget = argvbatch.Budget

// Split cuts files into argument lists that each start with prefix and stay
// within budget characters. See argvbatch.Split.
func Split(prefix, files []string, budget int) [][]string {
	return argvbatch.Split(prefix, files, budget)
}

// Batch calls run once per batch of paths within Budget and joins the outputs
// in order. See argvbatch.Run.
func Batch(prefix, paths []string, run func(args []string) (string, error)) (string, error) {
	return argvbatch.Run(prefix, paths, run)
}
