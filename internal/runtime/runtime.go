package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/example/go-code-agent/internal/config"
	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/model"
)

// BuildEnvironment creates an execution environment from normalized config.
func BuildEnvironment(cfg config.EnvironmentConfig, workspace string) (environment.Environment, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "local":
		return environment.NewLocalEnvironment(workspace)
	case "docker":
		return environment.NewDockerEnvironment(environment.DockerOptions{
			Image:              cfg.DockerImage,
			Workspace:          workspace,
			ContainerName:      cfg.ContainerName,
			ContainerWorkspace: cfg.ContainerWorkspace,
			KeepContainer:      cfg.KeepContainer,
		})
	default:
		return nil, fmt.Errorf("unknown environment type %q (supported: local, docker)", cfg.Type)
	}
}

// BuildModel creates a model client from normalized config.
func BuildModel(cfg config.ModelConfig) (model.Client, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "", "openai", "openai-compatible", "openai_compatible":
		apiKey := os.Getenv(cfg.APIKeyEnv)
		if apiKey == "" {
			return nil, fmt.Errorf("missing API key: env var %s is empty", cfg.APIKeyEnv)
		}
		return model.NewOpenAICompatClient(cfg.BaseURL, apiKey, cfg.Model), nil
	case "anthropic":
		apiKeyEnv := cfg.APIKeyEnv
		if apiKeyEnv == "" || apiKeyEnv == "OPENAI_API_KEY" {
			apiKeyEnv = "ANTHROPIC_API_KEY"
		}
		apiKey := os.Getenv(apiKeyEnv)
		if apiKey == "" {
			return nil, fmt.Errorf("missing API key: env var %s is empty", apiKeyEnv)
		}
		baseURL := cfg.BaseURL
		if baseURL == "" || baseURL == "https://api.openai.com/v1" {
			baseURL = "https://api.anthropic.com/v1"
		}
		return model.NewAnthropicClient(baseURL, apiKey, cfg.Model), nil
	case "replay":
		return model.NewReplayClient(cfg.ReplayFile)
	case "human":
		return model.NewHumanClient(), nil
	default:
		return nil, fmt.Errorf("unknown model provider %q (supported: openai-compatible, anthropic, replay, human)", cfg.Provider)
	}
}

// ResolveTask loads task content from either inline text or a file.
func ResolveTask(task, taskFile string) (string, error) {
	if taskFile != "" {
		data, err := os.ReadFile(taskFile)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return task, nil
}
