package pkg

import "os"

// spawn is the raw spawn call: the same child, one layer lower.
func spawn(name string) (*os.Process, error) {
	return os.StartProcess(name, []string{name}, &os.ProcAttr{})
}
