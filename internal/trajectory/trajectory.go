package trajectory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/model"
)

// Run captures a complete agent execution for debugging/replay.
type Run struct {
	Format     string          `json:"format"`
	Version    string          `json:"version"`
	Task       string          `json:"task"`
	Workspace  string          `json:"workspace"`
	Model      string          `json:"model"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    time.Time       `json:"ended_at"`
	Status     string          `json:"status"`
	Submission string          `json:"submission,omitempty"`
	Stats      Stats           `json:"stats"`
	Messages   []model.Message `json:"messages"`
	Steps      []Step          `json:"steps"`
}

type Stats struct {
	Steps            int     `json:"steps"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	APICalls         int     `json:"api_calls"`
	DurationSeconds  float64 `json:"duration_seconds"`
}

type Step struct {
	Index            int                     `json:"index"`
	AssistantMessage string                  `json:"assistant_message"`
	Command          string                  `json:"command,omitempty"`
	Observation      *environment.ExecResult `json:"observation,omitempty"`
	ParseError       string                  `json:"parse_error,omitempty"`
	PolicyError      string                  `json:"policy_error,omitempty"`
	Usage            model.Usage             `json:"usage,omitempty"`
}

func Save(path string, run *Run) error {
	if path == "" {
		path = "trajectory.json"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
