package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFileYAMLSubset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codeagent.yaml")
	yaml := `
task: "fix the bug"
workspace: .
out: runs/test
print_patch: true
agent:
  max_steps: 7
  command_timeout: 3m
  max_observation_chars: 1234
  system_prompt: |
    You are a careful agent.
    Use bash.
model:
  provider: replay
  replay_file: replay.json
  temperature: 0.2
environment:
  type: docker
  image: python:3.12
  container_workspace: /repo
  keep_container: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Task != "fix the bug" || cfg.OutputDir != "runs/test" || !cfg.PrintPatch {
		t.Fatalf("unexpected top-level cfg: %+v", cfg)
	}
	if cfg.Agent.MaxSteps != 7 || cfg.Agent.CommandTimeout != 3*time.Minute || cfg.Agent.MaxObservationChars != 1234 {
		t.Fatalf("unexpected agent cfg: %+v", cfg.Agent)
	}
	if cfg.Agent.SystemPrompt != "You are a careful agent.\nUse bash." {
		t.Fatalf("unexpected system prompt: %q", cfg.Agent.SystemPrompt)
	}
	if cfg.Model.Provider != "replay" || cfg.Model.ReplayFile != "replay.json" || cfg.Model.Temperature != 0.2 {
		t.Fatalf("unexpected model cfg: %+v", cfg.Model)
	}
	if cfg.Environment.Type != "docker" || cfg.Environment.DockerImage != "python:3.12" || cfg.Environment.ContainerWorkspace != "/repo" || !cfg.Environment.KeepContainer {
		t.Fatalf("unexpected env cfg: %+v", cfg.Environment)
	}
}

func TestLoadDotEnvDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("CODEAGENT_MODEL=file-value\nCODEAGENT_BASE_URL=http://local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEAGENT_MODEL", "process-value")
	if err := LoadDotEnv(path, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("CODEAGENT_MODEL"); got != "process-value" {
		t.Fatalf("CODEAGENT_MODEL=%q", got)
	}
	if got := os.Getenv("CODEAGENT_BASE_URL"); got != "http://local" {
		t.Fatalf("CODEAGENT_BASE_URL=%q", got)
	}
}
