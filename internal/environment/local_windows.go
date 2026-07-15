//go:build windows

package environment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func localShellName() string { return "powershell" }

func newLocalCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	)
}

func configureLocalCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return terminateLocalProcess(cmd.Process)
	}
}

func terminateLocalProcess(process *os.Process) error {
	// os/exec terminates the direct PowerShell process when the context expires.
	// taskkill /T additionally cleans up any child processes it started.
	output, err := exec.Command("taskkill.exe", "/PID", fmt.Sprint(process.Pid), "/T", "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill process tree: %w: %s", err, output)
	}
	return nil
}
