package pkg

import (
	"context"
	"os/exec"
)

// build starts the go tool under a context, which ends only the direct child.
func build(ctx context.Context) error {
	return exec.CommandContext(ctx, "go", "build", "./...").Run()
}
