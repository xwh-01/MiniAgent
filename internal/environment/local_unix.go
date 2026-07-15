//go:build !windows

package environment

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func localShellName() string { return "sh" }

func newLocalCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-lc", command)
}

func configureLocalCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return terminateLocalProcess(cmd.Process)
	}
}

func terminateLocalProcess(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}
