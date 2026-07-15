package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the normalized application configuration.
// It intentionally mirrors CLI concepts so flags, environment variables, and
// YAML config can all resolve into one structure.
type Config struct {
	Task            string `json:"task"`
	TaskFile        string `json:"task_file"`
	Workspace       string `json:"workspace"`
	OutputDir       string `json:"out"`
	PrintTrajectory bool   `json:"print_trajectory"`
	PrintPatch      bool   `json:"print_patch"`

	Agent       AgentConfig       `json:"agent"`
	Model       ModelConfig       `json:"model"`
	Environment EnvironmentConfig `json:"environment"`
}

type AgentConfig struct {
	MaxSteps            int           `json:"max_steps"`
	CommandTimeout      time.Duration `json:"command_timeout"`
	MaxObservationChars int           `json:"max_observation_chars"`
	SystemPrompt        string        `json:"system_prompt"`
	SystemPromptFile    string        `json:"system_prompt_file"`
}

type ModelConfig struct {
	Provider    string  `json:"provider"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	APIKeyEnv   string  `json:"api_key_env"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	ReplayFile  string  `json:"replay_file"`
}

type EnvironmentConfig struct {
	Type               string `json:"type"`
	DockerImage        string `json:"image"`
	ContainerName      string `json:"container_name"`
	ContainerWorkspace string `json:"container_workspace"`
	KeepContainer      bool   `json:"keep_container"`
}

// Default returns defaults after environment variables have already been loaded.
func Default() Config {
	provider := getenv("CODEAGENT_MODEL_PROVIDER", "openai-compatible")
	apiKeyEnv := getenv("CODEAGENT_API_KEY_ENV", "OPENAI_API_KEY")
	if provider == "anthropic" && os.Getenv("CODEAGENT_API_KEY_ENV") == "" {
		apiKeyEnv = "ANTHROPIC_API_KEY"
	}
	return Config{
		Workspace:       getenv("CODEAGENT_WORKSPACE", "."),
		OutputDir:       getenv("CODEAGENT_OUT", "runs/latest"),
		PrintTrajectory: getenvBool("CODEAGENT_PRINT_TRAJECTORY", false),
		PrintPatch:      getenvBool("CODEAGENT_PRINT_PATCH", false),
		Agent: AgentConfig{
			MaxSteps:            getenvInt("CODEAGENT_MAX_STEPS", 50),
			CommandTimeout:      getenvDuration("CODEAGENT_COMMAND_TIMEOUT", 120*time.Second),
			MaxObservationChars: getenvInt("CODEAGENT_MAX_OBSERVATION_CHARS", 20_000),
			SystemPrompt:        os.Getenv("CODEAGENT_SYSTEM_PROMPT"),
			SystemPromptFile:    os.Getenv("CODEAGENT_SYSTEM_PROMPT_FILE"),
		},
		Model: ModelConfig{
			Provider:    provider,
			BaseURL:     getenv("CODEAGENT_BASE_URL", "https://api.openai.com/v1"),
			Model:       getenv("CODEAGENT_MODEL", "gpt-4.1-mini"),
			APIKeyEnv:   apiKeyEnv,
			Temperature: getenvFloat("CODEAGENT_TEMPERATURE", 0),
			MaxTokens:   getenvInt("CODEAGENT_MAX_TOKENS", 4096),
			ReplayFile:  os.Getenv("CODEAGENT_REPLAY_FILE"),
		},
		Environment: EnvironmentConfig{
			Type:               getenv("CODEAGENT_ENV", "local"),
			DockerImage:        os.Getenv("CODEAGENT_DOCKER_IMAGE"),
			ContainerName:      os.Getenv("CODEAGENT_CONTAINER_NAME"),
			ContainerWorkspace: getenv("CODEAGENT_CONTAINER_WORKSPACE", "/workspace"),
			KeepContainer:      getenvBool("CODEAGENT_KEEP_CONTAINER", false),
		},
	}
}

// LoadDotEnv loads KEY=VALUE pairs. Existing env vars win unless override is true.
func LoadDotEnv(path string, override bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.Index(line, "=")
		if idx < 0 {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = trimQuotes(val)
		if key == "" {
			return fmt.Errorf("%s:%d: empty env key", path, lineNo)
		}
		if !override {
			if _, exists := os.LookupEnv(key); exists {
				continue
			}
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return s.Err()
}

func GlobalDotEnvPath() string {
	if custom := os.Getenv("CODEAGENT_GLOBAL_CONFIG_DIR"); custom != "" {
		return filepath.Join(custom, ".env")
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "go-code-agent", ".env")
}

func EnsureGlobalConfigDir() error {
	p := GlobalDotEnvPath()
	if p == "" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(p), 0o755)
}

func ExistingDefaultConfigPath() string {
	for _, p := range []string{"codeagent.yaml", "codeagent.yml", ".codeagent.yaml", ".codeagent.yml"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func LoadFile(path string, base Config) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return base, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return base, err
	}
	trim := strings.TrimSpace(string(data))
	if trim == "" {
		return base, nil
	}
	if strings.HasPrefix(trim, "{") {
		var fileCfg Config
		if err := json.Unmarshal(data, &fileCfg); err != nil {
			return base, err
		}
		ApplyConfig(&base, fileCfg)
		return base, nil
	}
	flat, err := parseYAMLSubset(string(data))
	if err != nil {
		return base, err
	}
	if err := ApplyFlat(&base, flat); err != nil {
		return base, err
	}
	return base, nil
}

func ApplyConfig(dst *Config, src Config) {
	if src.Task != "" {
		dst.Task = src.Task
	}
	if src.TaskFile != "" {
		dst.TaskFile = src.TaskFile
	}
	if src.Workspace != "" {
		dst.Workspace = src.Workspace
	}
	if src.OutputDir != "" {
		dst.OutputDir = src.OutputDir
	}
	// bool zero values are ambiguous in JSON struct overlays, so prefer YAML or flags for bools.
	if src.PrintTrajectory {
		dst.PrintTrajectory = true
	}
	if src.PrintPatch {
		dst.PrintPatch = true
	}
	if src.Agent.MaxSteps != 0 {
		dst.Agent.MaxSteps = src.Agent.MaxSteps
	}
	if src.Agent.CommandTimeout != 0 {
		dst.Agent.CommandTimeout = src.Agent.CommandTimeout
	}
	if src.Agent.MaxObservationChars != 0 {
		dst.Agent.MaxObservationChars = src.Agent.MaxObservationChars
	}
	if src.Agent.SystemPrompt != "" {
		dst.Agent.SystemPrompt = src.Agent.SystemPrompt
	}
	if src.Agent.SystemPromptFile != "" {
		dst.Agent.SystemPromptFile = src.Agent.SystemPromptFile
	}
	if src.Model.Provider != "" {
		dst.Model.Provider = src.Model.Provider
	}
	if src.Model.BaseURL != "" {
		dst.Model.BaseURL = src.Model.BaseURL
	}
	if src.Model.Model != "" {
		dst.Model.Model = src.Model.Model
	}
	if src.Model.APIKeyEnv != "" {
		dst.Model.APIKeyEnv = src.Model.APIKeyEnv
	}
	if src.Model.Temperature != 0 {
		dst.Model.Temperature = src.Model.Temperature
	}
	if src.Model.MaxTokens != 0 {
		dst.Model.MaxTokens = src.Model.MaxTokens
	}
	if src.Model.ReplayFile != "" {
		dst.Model.ReplayFile = src.Model.ReplayFile
	}
	if src.Environment.Type != "" {
		dst.Environment.Type = src.Environment.Type
	}
	if src.Environment.DockerImage != "" {
		dst.Environment.DockerImage = src.Environment.DockerImage
	}
	if src.Environment.ContainerName != "" {
		dst.Environment.ContainerName = src.Environment.ContainerName
	}
	if src.Environment.ContainerWorkspace != "" {
		dst.Environment.ContainerWorkspace = src.Environment.ContainerWorkspace
	}
	if src.Environment.KeepContainer {
		dst.Environment.KeepContainer = true
	}
}

func ApplyFlat(c *Config, flat map[string]string) error {
	for key, val := range flat {
		switch normalizeKey(key) {
		case "task":
			c.Task = val
		case "task_file", "taskfile":
			c.TaskFile = val
		case "workspace":
			c.Workspace = val
		case "out", "output_dir", "outputdir":
			c.OutputDir = val
		case "print_trajectory", "printtrajectory":
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.PrintTrajectory = b
		case "print_patch", "printpatch":
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.PrintPatch = b
		case "agent.max_steps", "agent.maxsteps":
			i, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Agent.MaxSteps = i
		case "agent.command_timeout", "agent.commandtimeout":
			d, err := parseDuration(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Agent.CommandTimeout = d
		case "agent.max_observation_chars", "agent.maxobservationchars":
			i, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Agent.MaxObservationChars = i
		case "agent.system_prompt", "agent.systemprompt":
			c.Agent.SystemPrompt = val
		case "agent.system_prompt_file", "agent.systempromptfile":
			c.Agent.SystemPromptFile = val
		case "model.provider":
			c.Model.Provider = val
		case "model.base_url", "model.baseurl":
			c.Model.BaseURL = val
		case "model.model", "model.name":
			c.Model.Model = val
		case "model.api_key_env", "model.apikeyenv":
			c.Model.APIKeyEnv = val
		case "model.temperature":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Model.Temperature = f
		case "model.max_tokens", "model.maxtokens":
			i, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Model.MaxTokens = i
		case "model.replay_file", "model.replayfile":
			c.Model.ReplayFile = val
		case "environment.type", "env":
			c.Environment.Type = val
		case "environment.image", "environment.docker_image", "environment.dockerimage", "image":
			c.Environment.DockerImage = val
		case "environment.container_name", "environment.containername":
			c.Environment.ContainerName = val
		case "environment.container_workspace", "environment.containerworkspace":
			c.Environment.ContainerWorkspace = val
		case "environment.keep_container", "environment.keepcontainer":
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			c.Environment.KeepContainer = b
		default:
			return fmt.Errorf("unknown config key %q", key)
		}
	}
	return nil
}

func LoadSystemPrompt(c *Config) error {
	if strings.TrimSpace(c.Agent.SystemPromptFile) == "" {
		return nil
	}
	data, err := os.ReadFile(c.Agent.SystemPromptFile)
	if err != nil {
		return err
	}
	c.Agent.SystemPrompt = string(data)
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := parseBool(v); err == nil {
			return b
		}
	}
	return def
}
func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := parseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func parseBool(s string) (bool, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool %q", s)
	}
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	seconds, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func normalizeKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "_")
	return strings.ToLower(s)
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			unq, err := strconv.Unquote(s)
			if err == nil {
				return unq
			}
			return s[1 : len(s)-1]
		}
	}
	return s
}
