package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// LocalEnvironment executes commands on the host. Prefer Docker for untrusted tasks.
type LocalEnvironment struct {
	workspace string
}

func NewLocalEnvironment(workspace string) (*LocalEnvironment, error) {
	if workspace == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return &LocalEnvironment{workspace: abs}, nil
}

func (e *LocalEnvironment) Workspace() string { return e.workspace }
func (e *LocalEnvironment) Shell() string     { return localShellName() }
func (e *LocalEnvironment) Close() error      { return nil }

func (e *LocalEnvironment) Execute(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	if req.Command == "" {
		return nil, errors.New("empty command")
	}
	if req.Timeout <= 0 {
		req.Timeout = 120 * time.Second
	}
	cwd := req.CWD
	if cwd == "" {
		cwd = e.workspace
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(e.workspace, cwd)
	}

	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd := newLocalCommand(ctx, req.Command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	configureLocalCommand(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	waitErr := cmd.Wait()
	timedOut := ctx.Err() == context.DeadlineExceeded
	if timedOut && cmd.Process != nil {
		// Kill the process tree to avoid orphaned child processes.
		_ = terminateLocalProcess(cmd.Process)
	}

	exitCode := 0
	if waitErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	res := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: time.Since(start),
		TimedOut: timedOut,
	}
	if waitErr != nil && !timedOut {
		res.Error = waitErr.Error()
	}
	if timedOut {
		res.Error = "command timed out"
	}
	return res, nil
}
