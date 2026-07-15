package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/go-code-agent/internal/agent"
	"github.com/example/go-code-agent/internal/config"
	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/runtime"
	"github.com/example/go-code-agent/internal/trajectory"
)

// Options controls a benchmark run.
type Options struct {
	BaseConfig      config.Config
	OutputDir       string
	Concurrency     int
	Limit           int
	ContinueOnError bool
	KeepWorkspaces  bool
}

// CommandResult records setup/test command execution.
type CommandResult struct {
	Command  string                     `json:"command"`
	Result   *environment.ExecResult    `json:"result,omitempty"`
	Error    string                     `json:"error,omitempty"`
	Phase    string                     `json:"phase"`
	Index    int                        `json:"index"`
	Started  time.Time                  `json:"started_at"`
	Ended    time.Time                  `json:"ended_at"`
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
}

// Result is written as result.json for each task.
type Result struct {
	TaskID          string          `json:"task_id"`
	Status          string          `json:"status"`
	Resolved        bool            `json:"resolved"`
	TestPassed      bool            `json:"test_passed"`
	Error           string          `json:"error,omitempty"`
	Steps           int             `json:"steps"`
	DurationSeconds float64         `json:"duration_seconds"`
	RunDir          string          `json:"run_dir"`
	Workspace       string          `json:"workspace"`
	TrajectoryPath  string          `json:"trajectory_path"`
	PatchPath       string          `json:"patch_path"`
	ResultPath      string          `json:"result_path"`
	Setup           []CommandResult `json:"setup,omitempty"`
	Tests           []CommandResult `json:"tests,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	EndedAt         time.Time       `json:"ended_at"`
}

// Summary is written at the end of the benchmark run.
type Summary struct {
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	DurationSeconds float64   `json:"duration_seconds"`
	Total           int       `json:"total"`
	Resolved        int       `json:"resolved"`
	Failed          int       `json:"failed"`
	Errored         int       `json:"errored"`
	OutputDir       string    `json:"output_dir"`
	Concurrency     int       `json:"concurrency"`
	Results         []Result  `json:"results"`
}

// Run executes benchmark tasks concurrently and writes per-task artifacts plus summary.json.
func Run(ctx context.Context, taskFiles []string, opts Options) (Summary, error) {
	started := time.Now()
	if opts.OutputDir == "" {
		opts.OutputDir = "runs/bench"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return Summary{}, err
	}

	tasks := make([]TaskSpec, 0, len(taskFiles))
	for _, p := range taskFiles {
		t, err := LoadTaskFile(p)
		if err != nil {
			return Summary{}, err
		}
		tasks = append(tasks, t)
	}
	if opts.Limit > 0 && opts.Limit < len(tasks) {
		tasks = tasks[:opts.Limit]
	}

	jobs := make(chan TaskSpec)
	results := make(chan Result, len(tasks))
	var wg sync.WaitGroup
	workerCount := opts.Concurrency
	if workerCount > len(tasks) && len(tasks) > 0 {
		workerCount = len(tasks)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				res := runOne(ctx, task, opts)
				results <- res
				if res.Error != "" && !opts.ContinueOnError {
					cancel()
				}
			}
		}()
	}

	sendErr := error(nil)
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			sendErr = ctx.Err()
			break
		case jobs <- task:
		}
		if sendErr != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	collected := make([]Result, 0, len(tasks))
	for res := range results {
		collected = append(collected, res)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].TaskID < collected[j].TaskID })

	sum := Summary{
		StartedAt:       started,
		EndedAt:         time.Now(),
		DurationSeconds: time.Since(started).Seconds(),
		Total:           len(collected),
		OutputDir:       opts.OutputDir,
		Concurrency:     opts.Concurrency,
		Results:         collected,
	}
	for _, r := range collected {
		if r.Resolved {
			sum.Resolved++
		} else {
			sum.Failed++
		}
		if r.Error != "" {
			sum.Errored++
		}
	}
	if err := writeJSON(filepath.Join(opts.OutputDir, "summary.json"), sum); err != nil {
		return sum, err
	}
	if sendErr != nil && len(collected) < len(tasks) {
		return sum, sendErr
	}
	return sum, nil
}

func runOne(ctx context.Context, task TaskSpec, opts Options) Result {
	started := time.Now()
	id := safeID(task.ID)
	runDir := filepath.Join(opts.OutputDir, id)
	res := Result{TaskID: task.ID, Status: "running", RunDir: runDir, StartedAt: started}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return finishError(res, started, err)
	}
	res.ResultPath = filepath.Join(runDir, "result.json")
	res.TrajectoryPath = filepath.Join(runDir, "trajectory.json")
	res.PatchPath = filepath.Join(runDir, "patch.diff")
	_ = writeJSON(filepath.Join(runDir, "task.json"), task)

	cfg := opts.BaseConfig
	if task.ConfigFile != "" {
		loaded, err := config.LoadFile(task.ConfigFile, cfg)
		if err != nil {
			return finishAndWrite(finishError(res, started, fmt.Errorf("load task config: %w", err)))
		}
		cfg = loaded
	}

	workspace, err := prepareWorkspace(ctx, task, runDir)
	if err != nil {
		return finishAndWrite(finishError(res, started, err))
	}
	res.Workspace = workspace
	cfg.Workspace = workspace
	cfg.OutputDir = runDir

	execEnv, err := runtime.BuildEnvironment(cfg.Environment, workspace)
	if err != nil {
		return finishAndWrite(finishError(res, started, err))
	}
	defer execEnv.Close()

	for i, cmd := range task.Setup {
		cr := runCommand(ctx, execEnv, "setup", i+1, cmd, cfg.Agent.CommandTimeout)
		res.Setup = append(res.Setup, cr)
		if cr.Error != "" || cr.Result == nil || cr.Result.ExitCode != 0 {
			return finishAndWrite(finishFailure(res, started, "setup_failed", fmt.Errorf("setup command %d failed", i+1)))
		}
	}

	client, err := runtime.BuildModel(cfg.Model)
	if err != nil {
		return finishAndWrite(finishError(res, started, err))
	}

	runner := &agent.Runner{Model: client, Env: execEnv}
	agentResult, err := runner.Run(ctx, task.ProblemStatement, agent.Options{
		MaxSteps:            cfg.Agent.MaxSteps,
		CommandTimeout:      cfg.Agent.CommandTimeout,
		MaxObservationChars: cfg.Agent.MaxObservationChars,
		Temperature:         cfg.Model.Temperature,
		MaxTokens:           cfg.Model.MaxTokens,
		TrajectoryPath:      res.TrajectoryPath,
		PatchPath:           res.PatchPath,
		SystemPrompt:        cfg.Agent.SystemPrompt,
	})
	if agentResult != nil {
		res.Status = agentResult.Status
		res.Steps = agentResult.Steps
		if agentResult.Trajectory != nil {
			_ = trajectory.Save(res.TrajectoryPath, agentResult.Trajectory)
		}
		_ = os.WriteFile(res.PatchPath, []byte(agentResult.Patch), 0o644)
	}
	if err != nil {
		return finishAndWrite(finishError(res, started, err))
	}

	allTestsPassed := true
	for i, cmd := range task.Test {
		cr := runCommand(ctx, execEnv, "test", i+1, cmd, cfg.Agent.CommandTimeout)
		res.Tests = append(res.Tests, cr)
		if cr.Error != "" || cr.Result == nil || cr.Result.ExitCode != 0 {
			allTestsPassed = false
		}
	}
	if len(task.Test) == 0 {
		allTestsPassed = res.Status == "submitted"
	}
	res.TestPassed = allTestsPassed
	res.Resolved = res.Status == "submitted" && allTestsPassed
	if !res.Resolved && res.Status == "submitted" {
		res.Status = "tests_failed"
	}
	if !opts.KeepWorkspaces && task.Repo != "" {
		// Keep artifacts but remove cloned repo to avoid huge benchmark output directories.
		_ = os.RemoveAll(workspace)
	}
	res.EndedAt = time.Now()
	res.DurationSeconds = time.Since(started).Seconds()
	return finishAndWrite(res)
}

func prepareWorkspace(ctx context.Context, task TaskSpec, runDir string) (string, error) {
	workspace := filepath.Join(runDir, "workspace")
	if task.Repo != "" {
		if err := runHostCommand(ctx, runDir, "git", "clone", task.Repo, workspace); err != nil {
			return "", fmt.Errorf("git clone: %w", err)
		}
		if task.BaseCommit != "" {
			if err := runHostCommand(ctx, workspace, "git", "checkout", task.BaseCommit); err != nil {
				return "", fmt.Errorf("git checkout %s: %w", task.BaseCommit, err)
			}
		}
		return workspace, nil
	}
	if task.Workspace != "" {
		abs, err := filepath.Abs(task.Workspace)
		if err != nil {
			return "", err
		}
		if err := copyDir(abs, workspace); err != nil {
			return "", fmt.Errorf("copy workspace: %w", err)
		}
		return workspace, nil
	}
	return "", fmt.Errorf("task %s: missing repo or workspace", task.ID)
}

func runCommand(ctx context.Context, env environment.Environment, phase string, idx int, command string, timeout time.Duration) CommandResult {
	started := time.Now()
	res, err := env.Execute(ctx, environment.ExecRequest{Command: command, Timeout: timeout})
	cr := CommandResult{Command: command, Result: res, Phase: phase, Index: idx, Started: started, Ended: time.Now()}
	if err != nil {
		cr.Error = err.Error()
	}
	return cr
}

func runHostCommand(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

var unsafeIDChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "task"
	}
	s = unsafeIDChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return "task"
	}
	return s
}

func finishError(res Result, started time.Time, err error) Result {
	res.Status = "error"
	res.Error = err.Error()
	res.EndedAt = time.Now()
	res.DurationSeconds = time.Since(started).Seconds()
	return res
}

func finishFailure(res Result, started time.Time, status string, err error) Result {
	res.Status = status
	if err != nil {
		res.Error = err.Error()
	}
	res.EndedAt = time.Now()
	res.DurationSeconds = time.Since(started).Seconds()
	return res
}

func finishAndWrite(res Result) Result {
	if res.ResultPath != "" {
		_ = writeJSON(res.ResultPath, res)
	}
	return res
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
