//go:build !unix

package cli

import "os/exec"

func configureWorkerExecutorCommand(cmd *exec.Cmd) {
}
