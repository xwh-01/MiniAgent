package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/example/go-code-agent/internal/action"
	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/gitutil"
	"github.com/example/go-code-agent/internal/model"
	"github.com/example/go-code-agent/internal/trajectory"
)

const SubmitSentinel = "COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT"

type Options struct {
	MaxSteps            int
	CommandTimeout      time.Duration
	MaxObservationChars int
	Temperature         float64
	MaxTokens           int
	TrajectoryPath      string
	PatchPath           string
	SystemPrompt        string
}

type Result struct {
	Status     string
	Steps      int
	Patch      string
	Trajectory *trajectory.Run
}

type Runner struct {
	Model model.Client
	Env   environment.Environment
}

func (r *Runner) Run(ctx context.Context, task string, opts Options) (*Result, error) {
	if r.Model == nil {
		return nil, fmt.Errorf("model is nil")
	}
	if r.Env == nil {
		return nil, fmt.Errorf("environment is nil")
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 50
	}
	if opts.CommandTimeout <= 0 {
		opts.CommandTimeout = 120 * time.Second
	}
	if opts.MaxObservationChars <= 0 {
		opts.MaxObservationChars = 20_000
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4096
	}
	if opts.SystemPrompt == "" {
		shell := ""
		if shellEnv, ok := r.Env.(environment.ShellEnvironment); ok {
			shell = shellEnv.Shell()
		}
		opts.SystemPrompt = DefaultSystemPromptForShell(shell)
	}

	messages := []model.Message{
		{Role: "system", Content: opts.SystemPrompt},
		{Role: "user", Content: task},
	}
	started := time.Now()
	run := &trajectory.Run{
		Format:    "go-code-agent-trajectory-v1",
		Version:   "0.1.0",
		Task:      task,
		Workspace: r.Env.Workspace(),
		Model:     r.Model.Name(),
		StartedAt: started,
		Status:    "running",
	}

	policy := action.Policy{MaxCommandChars: 20_000}
	status := "max_steps_exceeded"

	for step := 1; step <= opts.MaxSteps; step++ {
		resp, err := r.Model.Generate(ctx, messages, model.Options{
			Temperature: opts.Temperature,
			MaxTokens:   opts.MaxTokens,
		})
		if err != nil {
			status = "model_error"
			run.Status = status
			run.EndedAt = time.Now()
			run.Messages = messages
			return &Result{Status: status, Steps: step - 1, Trajectory: run}, err
		}

		run.Stats.APICalls++
		run.Stats.PromptTokens += resp.Usage.PromptTokens
		run.Stats.CompletionTokens += resp.Usage.CompletionTokens
		run.Stats.TotalTokens += resp.Usage.TotalTokens

		messages = append(messages, model.Message{Role: "assistant", Content: resp.Content})
		trStep := trajectory.Step{Index: step, AssistantMessage: resp.Content, Usage: resp.Usage}

		act, err := action.ParseBashBlock(resp.Content)
		if err != nil {
			trStep.ParseError = err.Error()
			run.Steps = append(run.Steps, trStep)
			messages = append(messages, model.Message{
				Role:    "user",
				Content: "Observation: I could not find an executable bash code block. Reply with exactly one fenced bash block, for example:\n\n```bash\nls -la\n```",
			})
			continue
		}

		trStep.Command = act.Command
		if err := policy.Check(act); err != nil {
			trStep.PolicyError = err.Error()
			run.Steps = append(run.Steps, trStep)
			messages = append(messages, model.Message{Role: "user", Content: "Observation: command blocked by policy: " + err.Error()})
			continue
		}

		execRes, execErr := r.Env.Execute(ctx, environment.ExecRequest{
			Command: act.Command,
			Timeout: opts.CommandTimeout,
		})
		if execErr != nil {
			status = "environment_error"
			run.Status = status
			run.EndedAt = time.Now()
			run.Messages = messages
			return &Result{Status: status, Steps: step - 1, Trajectory: run}, execErr
		}

		trStep.Observation = execRes
		run.Steps = append(run.Steps, trStep)

		obs := formatObservation(act.Command, execRes, opts.MaxObservationChars)
		messages = append(messages, model.Message{Role: "user", Content: obs})

		if action.IsSubmit(SubmitSentinel, act.Command, execRes.Stdout, execRes.Stderr) {
			status = "submitted"
			break
		}
	}

	patch := gitutil.Diff(ctx, r.Env.Workspace())
	run.Status = status
	run.Submission = patch
	run.EndedAt = time.Now()
	run.Stats.Steps = len(run.Steps)
	run.Stats.DurationSeconds = time.Since(started).Seconds()
	run.Messages = messages

	return &Result{Status: status, Steps: len(run.Steps), Patch: patch, Trajectory: run}, nil
}

func formatObservation(command string, res *environment.ExecResult, maxChars int) string {
	var b strings.Builder
	b.WriteString("Observation from executed command.\n")
	b.WriteString("\nCommand:\n")
	b.WriteString("```bash\n")
	b.WriteString(command)
	b.WriteString("\n```\n")
	b.WriteString(fmt.Sprintf("\nExit code: %d\n", res.ExitCode))
	b.WriteString(fmt.Sprintf("Timed out: %v\n", res.TimedOut))
	b.WriteString(fmt.Sprintf("Duration: %s\n", res.Duration))
	if res.Error != "" {
		b.WriteString("Error: " + res.Error + "\n")
	}
	b.WriteString("\nSTDOUT:\n")
	b.WriteString(truncate(res.Stdout, maxChars/2))
	b.WriteString("\n\nSTDERR:\n")
	b.WriteString(truncate(res.Stderr, maxChars/2))
	return b.String()
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [truncated %d bytes]", len(s)-max)
}
