package core

import "io"

// readFileShared reads the whole file at path like os.ReadFile, but on Windows
// opens it so that a writer can still replace it by rename while it is held.
// Every file the gate publishes with writeFileAtomic is read this way: a plain
// open there blocks the replace, and a reader that polls leaves it blocked.
func readFileShared(path string) ([]byte, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
