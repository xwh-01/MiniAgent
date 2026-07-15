package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultContainerWorkspace = "/workspace"

// DockerOptions configures DockerEnvironment.
type DockerOptions struct {
	Image              string
	Workspace          string
	ContainerName      string
	ContainerWorkspace string
	KeepContainer      bool
	StartupTimeout     time.Duration
}

// DockerEnvironment executes commands inside a long-lived Docker container.
// The host workspace is bind-mounted into ContainerWorkspace, so git diff can
// still be collected from the host while commands run in the sandbox.
type DockerEnvironment struct {
	image              string
	hostWorkspace      string
	containerWorkspace string
	containerName      string
	keepContainer      bool
	closed             bool
}

func NewDockerEnvironment(opts DockerOptions) (*DockerEnvironment, error) {
	if strings.TrimSpace(opts.Image) == "" {
		return nil, errors.New("docker image is required")
	}
	workspace := opts.Workspace
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

	containerWorkspace := opts.ContainerWorkspace
	if strings.TrimSpace(containerWorkspace) == "" {
		containerWorkspace = defaultContainerWorkspace
	}
	if !strings.HasPrefix(containerWorkspace, "/") {
		return nil, fmt.Errorf("container workspace must be absolute: %s", containerWorkspace)
	}

	name := opts.ContainerName
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("codeagent-%d", time.Now().UnixNano())
	}

	startupTimeout := opts.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = 60 * time.Second
	}

	e := &DockerEnvironment{
		image:              opts.Image,
		hostWorkspace:      abs,
		containerWorkspace: containerWorkspace,
		containerName:      name,
		keepContainer:      opts.KeepContainer,
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	if err := e.ensureDockerAvailable(ctx); err != nil {
		return nil, err
	}
	if err := e.startContainer(ctx); err != nil {
		_ = e.removeContainer(context.Background())
		return nil, err
	}
	return e, nil
}

func (e *DockerEnvironment) Workspace() string          { return e.hostWorkspace }
func (e *DockerEnvironment) Shell() string              { return "sh" }
func (e *DockerEnvironment) ContainerName() string      { return e.containerName }
func (e *DockerEnvironment) ContainerWorkspace() string { return e.containerWorkspace }

func (e *DockerEnvironment) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.keepContainer {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.removeContainer(ctx)
}

func (e *DockerEnvironment) Execute(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	if e.closed {
		return nil, errors.New("docker environment is closed")
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("empty command")
	}
	if req.Timeout <= 0 {
		req.Timeout = 120 * time.Second
	}

	cwd, err := e.containerCWD(req.CWD)
	if err != nil {
		return nil, err
	}

	execCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	args := []string{"exec", "-i", "-w", cwd}
	for k, v := range req.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, e.containerName, "/bin/sh", "-lc", req.Command)

	cmd := exec.CommandContext(execCtx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	waitErr := cmd.Run()
	timedOut := execCtx.Err() == context.DeadlineExceeded
	if timedOut {
		// docker exec does not reliably clean up the process tree in the
		// container when the client is killed. Restarting the container is blunt
		// but predictable, and the bind-mounted workspace changes are preserved.
		_ = e.restartContainer(context.Background())
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
		res.Error = "command timed out; docker container was restarted"
	}
	return res, nil
}

func (e *DockerEnvironment) ensureDockerAvailable(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("docker is not available: %s: %w", msg, err)
		}
		return fmt.Errorf("docker is not available: %w", err)
	}
	return nil
}

func (e *DockerEnvironment) startContainer(ctx context.Context) error {
	args := []string{
		"run", "-d",
		"--name", e.containerName,
		"-v", e.hostWorkspace + ":" + e.containerWorkspace,
		"-w", e.containerWorkspace,
		e.image,
		"/bin/sh", "-lc", "trap : TERM INT; while true; do sleep 3600 & wait $!; done",
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("start docker container: %s: %w", msg, err)
		}
		return fmt.Errorf("start docker container: %w", err)
	}
	return nil
}

func (e *DockerEnvironment) restartContainer(ctx context.Context) error {
	killCtx, cancelKill := context.WithTimeout(ctx, 20*time.Second)
	defer cancelKill()
	_ = exec.CommandContext(killCtx, "docker", "kill", e.containerName).Run()

	startCtx, cancelStart := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStart()
	cmd := exec.CommandContext(startCtx, "docker", "start", e.containerName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("restart docker container: %s: %w", msg, err)
		}
		return fmt.Errorf("restart docker container: %w", err)
	}
	return nil
}

func (e *DockerEnvironment) removeContainer(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", e.containerName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		// Treat already-removed containers as successfully cleaned up.
		if strings.Contains(strings.ToLower(msg), "no such container") {
			return nil
		}
		if msg != "" {
			return fmt.Errorf("remove docker container: %s: %w", msg, err)
		}
		return fmt.Errorf("remove docker container: %w", err)
	}
	return nil
}

func (e *DockerEnvironment) containerCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return e.containerWorkspace, nil
	}

	// Compare slash-normalized paths so this mapping also works when a Windows
	// host is targeting the Linux filesystem inside a container.
	cleanCWD := path.Clean(filepath.ToSlash(cwd))
	cleanHost := path.Clean(filepath.ToSlash(e.hostWorkspace))
	if pathsEqual(cleanCWD, cleanHost) {
		return e.containerWorkspace, nil
	}
	hostPrefix := strings.TrimRight(cleanHost, "/") + "/"
	if pathHasPrefix(cleanCWD, hostPrefix) {
		rel := cleanCWD[len(hostPrefix):]
		return pathJoinSlash(e.containerWorkspace, rel), nil
	}

	if filepath.IsAbs(cwd) || strings.HasPrefix(cleanCWD, "/") {
		// Already a container path or a deliberate absolute path inside image.
		return cleanCWD, nil
	}
	return pathJoinSlash(e.containerWorkspace, cleanCWD), nil
}

func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathHasPrefix(value, prefix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix))
	}
	return strings.HasPrefix(value, prefix)
}

func pathJoinSlash(base, rel string) string {
	base = strings.TrimRight(base, "/")
	rel = strings.TrimLeft(rel, "/")
	if rel == "." || rel == "" {
		return base
	}
	return base + "/" + rel
}
