package environment

import (
	"context"
	"time"
)

// ExecRequest describes one command execution.
type ExecRequest struct {
	Command string
	CWD     string
	Env     map[string]string
	Timeout time.Duration
}

// ExecResult is the normalized result of command execution.
type ExecResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
	TimedOut bool          `json:"timed_out"`
	Error    string        `json:"error,omitempty"`
}

// Environment executes commands inside some workspace/sandbox.
type Environment interface {
	Execute(ctx context.Context, req ExecRequest) (*ExecResult, error)
	Workspace() string
	Close() error
}

// ShellEnvironment optionally describes the command language accepted by an
// environment. Agents use it to produce platform-appropriate commands.
type ShellEnvironment interface {
	Shell() string
}
