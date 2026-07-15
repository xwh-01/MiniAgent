package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/example/go-code-agent/internal/agent"
	"github.com/example/go-code-agent/internal/config"
	"github.com/example/go-code-agent/internal/trajectory"
)

// SingleRunResult summarizes one codeagent run and points at durable artifacts.
type SingleRunResult struct {
	Status          string    `json:"status"`
	Steps           int       `json:"steps"`
	Workspace       string    `json:"workspace"`
	Model           string    `json:"model"`
	OutputDir       string    `json:"output_dir"`
	TrajectoryPath  string    `json:"trajectory_path"`
	PatchPath       string    `json:"patch_path"`
	ResultPath      string    `json:"result_path,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	DurationSeconds float64   `json:"duration_seconds"`
	Patch           string    `json:"-"`
}

// RunSingle executes a configured single-task agent run and writes trajectory/patch artifacts.
func RunSingle(ctx context.Context, cfg config.Config) (*SingleRunResult, error) {
	started := time.Now()
	if err := config.LoadSystemPrompt(&cfg); err != nil {
		return nil, fmt.Errorf("load system prompt: %w", err)
	}
	task, err := ResolveTask(cfg.Task, cfg.TaskFile)
	if err != nil {
		return nil, err
	}
	if task == "" {
		return nil, fmt.Errorf("missing task")
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "runs/latest"
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, err
	}

	execEnv, err := BuildEnvironment(cfg.Environment, cfg.Workspace)
	if err != nil {
		return nil, err
	}
	defer execEnv.Close()

	client, err := BuildModel(cfg.Model)
	if err != nil {
		return nil, err
	}

	trajectoryPath := filepath.Join(cfg.OutputDir, "trajectory.json")
	patchPath := filepath.Join(cfg.OutputDir, "patch.diff")
	runner := &agent.Runner{Model: client, Env: execEnv}
	agentResult, runErr := runner.Run(ctx, task, agent.Options{
		MaxSteps:            cfg.Agent.MaxSteps,
		CommandTimeout:      cfg.Agent.CommandTimeout,
		MaxObservationChars: cfg.Agent.MaxObservationChars,
		Temperature:         cfg.Model.Temperature,
		MaxTokens:           cfg.Model.MaxTokens,
		TrajectoryPath:      trajectoryPath,
		PatchPath:           patchPath,
		SystemPrompt:        cfg.Agent.SystemPrompt,
	})

	out := &SingleRunResult{
		Status:         "error",
		Workspace:      execEnv.Workspace(),
		Model:          client.Name(),
		OutputDir:      cfg.OutputDir,
		TrajectoryPath: trajectoryPath,
		PatchPath:      patchPath,
		ResultPath:     filepath.Join(cfg.OutputDir, "run.json"),
		StartedAt:      started,
		EndedAt:        time.Now(),
	}
	if agentResult != nil {
		out.Status = agentResult.Status
		out.Steps = agentResult.Steps
		out.Patch = agentResult.Patch
		if agentResult.Trajectory != nil {
			if err := trajectory.Save(trajectoryPath, agentResult.Trajectory); err != nil {
				return out, fmt.Errorf("save trajectory: %w", err)
			}
		}
		if err := os.WriteFile(patchPath, []byte(agentResult.Patch), 0o644); err != nil {
			return out, fmt.Errorf("save patch: %w", err)
		}
	}
	out.EndedAt = time.Now()
	out.DurationSeconds = out.EndedAt.Sub(started).Seconds()
	if err := writeResultJSON(out.ResultPath, out); err != nil {
		return out, fmt.Errorf("save run result: %w", err)
	}
	if runErr != nil {
		return out, runErr
	}
	return out, nil
}

func writeResultJSON(path string, v any) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := jsonMarshalIndent(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
