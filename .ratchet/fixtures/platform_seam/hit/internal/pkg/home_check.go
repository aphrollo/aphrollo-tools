package pkg

import "os"

// homeDir reads $HOME directly, which does not exist on Windows.
func homeDir() string {
	return os.Getenv("HOME")
}
