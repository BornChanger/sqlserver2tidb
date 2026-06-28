package cli

import (
	"context"
	"os/exec"
)

func newWorkerExecutorCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureWorkerExecutorCommand(cmd)
	return cmd
}
