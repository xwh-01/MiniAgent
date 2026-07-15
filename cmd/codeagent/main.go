package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/go-code-agent/internal/agent"
	"github.com/example/go-code-agent/internal/benchmark"
	"github.com/example/go-code-agent/internal/config"
	inspectpkg "github.com/example/go-code-agent/internal/inspect"
	reportpkg "github.com/example/go-code-agent/internal/report"
	"github.com/example/go-code-agent/internal/runtime"
	serverpkg "github.com/example/go-code-agent/internal/server"
	swebenchpkg "github.com/example/go-code-agent/internal/swebench"
	"github.com/example/go-code-agent/internal/trajectory"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "bench":
			runBenchCommand(os.Args[2:])
			return
		case "inspect":
			runInspectCommand(os.Args[2:])
			return
		case "report":
			runReportCommand(os.Args[2:])
			return
		case "swebench":
			runSWEbenchCommand(os.Args[2:])
			return
		case "serve":
			runServeCommand(os.Args[2:])
			return
		case "run":
			// Optional explicit subcommand; bare flags keep working for backwards compatibility.
			os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		}
	}

	flagValues, explicit := parseFlags()
	cfg, configPath, err := loadBaseConfig(flagValues.ConfigPath, explicit["config"])
	if err != nil {
		fatal(err)
	}
	applyExplicitFlags(&cfg, flagValues, explicit)
	if err := config.LoadSystemPrompt(&cfg); err != nil {
		fatal(fmt.Errorf("load system prompt: %w", err))
	}

	resolvedTask, err := runtime.ResolveTask(cfg.Task, cfg.TaskFile)
	if err != nil {
		fatal(err)
	}
	if strings.TrimSpace(resolvedTask) == "" {
		fatal(errors.New("missing task: pass --task, --task-file, or set task/task_file in config"))
	}

	execEnv, err := runtime.BuildEnvironment(cfg.Environment, cfg.Workspace)
	if err != nil {
		fatal(err)
	}
	defer execEnv.Close()

	client, err := runtime.BuildModel(cfg.Model)
	if err != nil {
		fatal(err)
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		fatal(err)
	}
	trajectoryPath := filepath.Join(cfg.OutputDir, "trajectory.json")
	patchPath := filepath.Join(cfg.OutputDir, "patch.diff")

	runner := &agent.Runner{Model: client, Env: execEnv}
	result, err := runner.Run(context.Background(), resolvedTask, agent.Options{
		MaxSteps:            cfg.Agent.MaxSteps,
		CommandTimeout:      cfg.Agent.CommandTimeout,
		MaxObservationChars: cfg.Agent.MaxObservationChars,
		Temperature:         cfg.Model.Temperature,
		MaxTokens:           cfg.Model.MaxTokens,
		TrajectoryPath:      trajectoryPath,
		PatchPath:           patchPath,
		SystemPrompt:        cfg.Agent.SystemPrompt,
	})
	if result != nil && result.Trajectory != nil {
		if saveErr := trajectory.Save(trajectoryPath, result.Trajectory); saveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: save trajectory: %v\n", saveErr)
		}
		if writeErr := os.WriteFile(patchPath, []byte(result.Patch), 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: save patch: %v\n", writeErr)
		}
	}
	if err != nil {
		fatal(err)
	}

	fmt.Printf("status=%s steps=%d\n", result.Status, result.Steps)
	fmt.Printf("workspace=%s\n", execEnv.Workspace())
	fmt.Printf("model=%s\n", client.Name())
	if configPath != "" {
		fmt.Printf("config=%s\n", configPath)
	}
	fmt.Printf("trajectory=%s\n", trajectoryPath)
	fmt.Printf("patch=%s\n", patchPath)
	if cfg.PrintTrajectory {
		fmt.Println(trajectoryPath)
	}
	if cfg.PrintPatch && result.Patch != "" {
		fmt.Println(result.Patch)
	}
}

func loadBaseConfig(configPath string, explicitConfig bool) (config.Config, string, error) {
	if err := config.EnsureGlobalConfigDir(); err != nil {
		return config.Config{}, "", err
	}
	// Load env files before defaults are computed. Existing process env vars win.
	if err := config.LoadDotEnv(config.GlobalDotEnvPath(), false); err != nil {
		return config.Config{}, "", fmt.Errorf("load global .env: %w", err)
	}
	if err := config.LoadDotEnv(".env", false); err != nil {
		return config.Config{}, "", fmt.Errorf("load local .env: %w", err)
	}
	cfg := config.Default()
	if configPath == "" && !explicitConfig {
		configPath = config.ExistingDefaultConfigPath()
	}
	loaded, err := config.LoadFile(configPath, cfg)
	if err != nil {
		return cfg, configPath, fmt.Errorf("load config: %w", err)
	}
	return loaded, configPath, nil
}

func runServeCommand(args []string) {
	fs := flag.NewFlagSet("codeagent serve", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to base YAML/JSON config file; defaults to codeagent.yaml if present")
	addr := fs.String("addr", ":8080", "HTTP listen address")
	out := fs.String("out", "runs/server", "Base output directory for server-submitted runs")
	workers := fs.Int("workers", 2, "Number of concurrent agent workers")
	queueSize := fs.Int("queue", 32, "Maximum queued jobs")
	fs.Parse(args)
	explicitConfig := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "config" {
			explicitConfig = true
		}
	})
	cfg, resolvedConfigPath, err := loadBaseConfig(*configPath, explicitConfig)
	if err != nil {
		fatal(err)
	}
	srv, err := serverpkg.New(serverpkg.Options{
		BaseConfig: cfg,
		OutputDir:  *out,
		Workers:    *workers,
		QueueSize:  *queueSize,
	})
	if err != nil {
		fatal(err)
	}
	if resolvedConfigPath != "" {
		fmt.Printf("config=%s\n", resolvedConfigPath)
	}
	fmt.Printf("listening=%s workers=%d out=%s\n", *addr, *workers, *out)
	fmt.Println("health=GET /health")
	fmt.Println("submit=POST /v1/runs")
	fmt.Println("status=GET /v1/runs/{id}")
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		fatal(err)
	}
}

func runBenchCommand(args []string) {
	if len(args) == 0 || args[0] != "run" {
		fatal(errors.New("usage: codeagent bench run [flags] <task.yaml|task.json>..."))
	}
	fs := flag.NewFlagSet("codeagent bench run", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to base YAML/JSON config file; defaults to codeagent.yaml if present")
	out := fs.String("out", "runs/bench", "Benchmark output directory")
	concurrency := fs.Int("concurrency", 1, "Number of tasks to run concurrently")
	limit := fs.Int("limit", 0, "Optional maximum number of task files to run")
	continueOnError := fs.Bool("continue-on-error", true, "Continue running other tasks after one task errors")
	keepWorkspaces := fs.Bool("keep-workspaces", false, "Keep cloned repo workspaces after each task")
	fs.Parse(args[1:])
	explicitConfig := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "config" {
			explicitConfig = true
		}
	})
	cfg, resolvedConfigPath, err := loadBaseConfig(*configPath, explicitConfig)
	if err != nil {
		fatal(err)
	}
	if err := config.LoadSystemPrompt(&cfg); err != nil {
		fatal(fmt.Errorf("load system prompt: %w", err))
	}
	taskFiles, err := expandTaskArgs(fs.Args())
	if err != nil {
		fatal(err)
	}
	if len(taskFiles) == 0 {
		fatal(errors.New("missing benchmark task files"))
	}
	summary, err := benchmark.Run(context.Background(), taskFiles, benchmark.Options{
		BaseConfig:      cfg,
		OutputDir:       *out,
		Concurrency:     *concurrency,
		Limit:           *limit,
		ContinueOnError: *continueOnError,
		KeepWorkspaces:  *keepWorkspaces,
	})
	if err != nil {
		fatal(err)
	}
	if resolvedConfigPath != "" {
		fmt.Printf("config=%s\n", resolvedConfigPath)
	}
	fmt.Printf("benchmark=%s total=%d resolved=%d failed=%d errored=%d\n", summary.OutputDir, summary.Total, summary.Resolved, summary.Failed, summary.Errored)
	fmt.Printf("summary=%s\n", filepath.Join(summary.OutputDir, "summary.json"))
}

func runSWEbenchCommand(args []string) {
	if len(args) == 0 {
		fatal(errors.New("usage: codeagent swebench <import|run|predictions> [flags]"))
	}
	switch args[0] {
	case "import":
		runSWEbenchImportCommand(args[1:])
	case "run":
		runSWEbenchRunCommand(args[1:])
	case "predictions":
		runSWEbenchPredictionsCommand(args[1:])
	default:
		fatal(fmt.Errorf("unknown swebench subcommand %q", args[0]))
	}
}

func runSWEbenchImportCommand(args []string) {
	fs := flag.NewFlagSet("codeagent swebench import", flag.ExitOnError)
	out := fs.String("out", "tasks/swebench", "Output directory for converted task JSON files")
	taskConfig := fs.String("task-config", "", "Optional config file path embedded into every generated task")
	appendHints := fs.Bool("append-hints", false, "Append hints_text to the problem statement")
	defaultRepoURL := fs.String("github-base-url", "https://github.com", "Base URL used when repo is owner/name")
	var tests stringListFlag
	fs.Var(&tests, "test", "Acceptance test command to add to each generated task; may be repeated")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(errors.New("usage: codeagent swebench import [flags] <instances.json|instances.jsonl>"))
	}
	instances, err := swebenchpkg.LoadInstances(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	paths, err := swebenchpkg.WriteTasks(instances, *out, swebenchpkg.ConvertOptions{
		ConfigFile:     *taskConfig,
		AppendHints:    *appendHints,
		TestCommands:   []string(tests),
		DefaultRepoURL: *defaultRepoURL,
	})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("instances=%d tasks_dir=%s\n", len(instances), *out)
	if len(paths) > 0 {
		fmt.Printf("first_task=%s\n", paths[0])
	}
}

func runSWEbenchRunCommand(args []string) {
	fs := flag.NewFlagSet("codeagent swebench run", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to base YAML/JSON config file; defaults to codeagent.yaml if present")
	out := fs.String("out", "runs/swebench", "SWE-bench run output directory")
	tasksDir := fs.String("tasks-dir", "", "Directory for generated internal task specs; defaults to <out>/_tasks")
	taskConfig := fs.String("task-config", "", "Optional config file path embedded into every generated task")
	modelName := fs.String("model-name", "", "model_name_or_path for SWE-bench predictions; defaults to configured model")
	predictionsPath := fs.String("predictions", "", "Predictions JSON output path; defaults to <out>/predictions.json")
	predictionsJSONLPath := fs.String("predictions-jsonl", "", "Optional predictions JSONL output path")
	concurrency := fs.Int("concurrency", 1, "Number of instances to run concurrently")
	limit := fs.Int("limit", 0, "Optional maximum number of instances")
	continueOnError := fs.Bool("continue-on-error", true, "Continue running other instances after one errors")
	keepWorkspaces := fs.Bool("keep-workspaces", false, "Keep cloned repo workspaces after each instance")
	appendHints := fs.Bool("append-hints", false, "Append hints_text to the problem statement")
	defaultRepoURL := fs.String("github-base-url", "https://github.com", "Base URL used when repo is owner/name")
	var tests stringListFlag
	fs.Var(&tests, "test", "Acceptance test command to run after each agent submission; may be repeated")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(errors.New("usage: codeagent swebench run [flags] <instances.json|instances.jsonl>"))
	}
	explicitConfig := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "config" {
			explicitConfig = true
		}
	})
	cfg, resolvedConfigPath, err := loadBaseConfig(*configPath, explicitConfig)
	if err != nil {
		fatal(err)
	}
	if err := config.LoadSystemPrompt(&cfg); err != nil {
		fatal(fmt.Errorf("load system prompt: %w", err))
	}
	name := strings.TrimSpace(*modelName)
	if name == "" {
		name = cfg.Model.Model
	}
	summary, preds, err := swebenchpkg.Run(context.Background(), fs.Arg(0), swebenchpkg.RunOptions{
		BaseConfig:      cfg,
		OutputDir:       *out,
		TasksDir:        *tasksDir,
		TaskConfigFile:  *taskConfig,
		ModelName:       name,
		Concurrency:     *concurrency,
		Limit:           *limit,
		ContinueOnError: *continueOnError,
		KeepWorkspaces:  *keepWorkspaces,
		AppendHints:     *appendHints,
		TestCommands:    tests,
		DefaultRepoURL:  *defaultRepoURL,
	})
	if err != nil {
		fatal(err)
	}
	predPath := *predictionsPath
	if predPath == "" {
		predPath = filepath.Join(*out, "predictions.json")
	}
	if err := swebenchpkg.SavePredictionsJSON(predPath, preds); err != nil {
		fatal(err)
	}
	if *predictionsJSONLPath != "" {
		if err := swebenchpkg.SavePredictionsJSONL(*predictionsJSONLPath, preds); err != nil {
			fatal(err)
		}
	}
	if resolvedConfigPath != "" {
		fmt.Printf("config=%s\n", resolvedConfigPath)
	}
	fmt.Printf("swebench=%s total=%d resolved=%d failed=%d errored=%d\n", summary.OutputDir, summary.Total, summary.Resolved, summary.Failed, summary.Errored)
	fmt.Printf("summary=%s\n", filepath.Join(summary.OutputDir, "summary.json"))
	fmt.Printf("predictions=%s\n", predPath)
	if *predictionsJSONLPath != "" {
		fmt.Printf("predictions_jsonl=%s\n", *predictionsJSONLPath)
	}
}

func runSWEbenchPredictionsCommand(args []string) {
	fs := flag.NewFlagSet("codeagent swebench predictions", flag.ExitOnError)
	out := fs.String("out", "", "Predictions output path; defaults to <bench-dir>/predictions.json")
	jsonlOut := fs.String("jsonl", "", "Optional JSONL output path")
	modelName := fs.String("model-name", "go-code-agent", "model_name_or_path for predictions")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(errors.New("usage: codeagent swebench predictions [flags] <benchmark-output-dir>"))
	}
	benchDir := fs.Arg(0)
	preds, err := swebenchpkg.BuildPredictions(benchDir, *modelName)
	if err != nil {
		fatal(err)
	}
	path := *out
	if path == "" {
		path = filepath.Join(benchDir, "predictions.json")
	}
	if err := swebenchpkg.SavePredictionsJSON(path, preds); err != nil {
		fatal(err)
	}
	if *jsonlOut != "" {
		if err := swebenchpkg.SavePredictionsJSONL(*jsonlOut, preds); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("predictions=%s count=%d\n", path, len(preds))
	if *jsonlOut != "" {
		fmt.Printf("predictions_jsonl=%s\n", *jsonlOut)
	}
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }

func (s *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*s = append(*s, value)
	}
	return nil
}

func runInspectCommand(args []string) {
	fs := flag.NewFlagSet("codeagent inspect", flag.ExitOnError)
	step := fs.Int("step", 0, "Show only a specific 1-based step")
	messages := fs.Bool("messages", false, "Show raw conversation messages instead of step summary")
	full := fs.Bool("full", false, "Do not truncate long fields")
	maxChars := fs.Int("max-chars", 2000, "Maximum characters per rendered field unless --full is set")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(errors.New("usage: codeagent inspect [flags] <trajectory.json>"))
	}
	run, err := trajectory.Load(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	if err := inspectpkg.Render(os.Stdout, run, inspectpkg.Options{
		Step:          *step,
		ShowMessages:  *messages,
		ShowFull:      *full,
		MaxFieldChars: *maxChars,
	}); err != nil {
		fatal(err)
	}
}

func runReportCommand(args []string) {
	fs := flag.NewFlagSet("codeagent report", flag.ExitOnError)
	jsonOut := fs.String("json", "", "Optional report JSON path; defaults to <bench-dir>/report.json")
	htmlOut := fs.String("html", "", "Optional report HTML path; defaults to <bench-dir>/report.html")
	noHTML := fs.Bool("no-html", false, "Do not write an HTML report")
	noJSON := fs.Bool("no-json", false, "Do not write report.json")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fatal(errors.New("usage: codeagent report [flags] <benchmark-output-dir>"))
	}
	benchDir := fs.Arg(0)
	rep, err := reportpkg.Build(benchDir)
	if err != nil {
		fatal(err)
	}
	if err := reportpkg.RenderText(os.Stdout, rep); err != nil {
		fatal(err)
	}
	if !*noJSON {
		path := *jsonOut
		if path == "" {
			path = filepath.Join(benchDir, "report.json")
		}
		if err := reportpkg.SaveJSON(path, rep); err != nil {
			fatal(err)
		}
		fmt.Printf("report_json=%s\n", path)
	}
	if !*noHTML {
		path := *htmlOut
		if path == "" {
			path = filepath.Join(benchDir, "report.html")
		}
		if err := reportpkg.SaveHTML(path, rep); err != nil {
			fatal(err)
		}
		fmt.Printf("report_html=%s\n", path)
	}
}

func expandTaskArgs(args []string) ([]string, error) {
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			patterns := []string{filepath.Join(arg, "*.yaml"), filepath.Join(arg, "*.yml"), filepath.Join(arg, "*.json")}
			for _, pat := range patterns {
				matches, err := filepath.Glob(pat)
				if err != nil {
					return nil, err
				}
				files = append(files, matches...)
			}
			continue
		}
		if err != nil && os.IsNotExist(err) {
			matches, globErr := filepath.Glob(arg)
			if globErr != nil {
				return nil, globErr
			}
			if len(matches) > 0 {
				files = append(files, matches...)
				continue
			}
		}
		files = append(files, arg)
	}
	return files, nil
}

type cliFlags struct {
	ConfigPath          string
	Task                string
	TaskFile            string
	Workspace           string
	OutputDir           string
	EnvType             string
	DockerImage         string
	ContainerName       string
	ContainerWorkspace  string
	KeepContainer       bool
	ModelProvider       string
	BaseURL             string
	ModelName           string
	APIKeyEnv           string
	ReplayFile          string
	MaxSteps            int
	CommandTimeout      time.Duration
	MaxObservationChars int
	Temperature         float64
	MaxTokens           int
	SystemPrompt        string
	SystemPromptFile    string
	PrintTrajectory     bool
	PrintPatch          bool
}

func parseFlags() (cliFlags, map[string]bool) {
	var f cliFlags
	flag.StringVar(&f.ConfigPath, "config", "", "Path to YAML/JSON config file; defaults to codeagent.yaml if present")
	flag.StringVar(&f.Task, "task", "", "Task text for the agent")
	flag.StringVar(&f.TaskFile, "task-file", "", "Path to a file containing the task")
	flag.StringVar(&f.Workspace, "workspace", "", "Workspace/repository directory")
	flag.StringVar(&f.OutputDir, "out", "", "Output directory for trajectory and patch")
	flag.StringVar(&f.EnvType, "env", "", "Execution environment: local or docker")
	flag.StringVar(&f.DockerImage, "image", "", "Docker image for --env docker")
	flag.StringVar(&f.ContainerName, "container-name", "", "Optional Docker container name")
	flag.StringVar(&f.ContainerWorkspace, "container-workspace", "", "Workspace path inside Docker container")
	flag.BoolVar(&f.KeepContainer, "keep-container", false, "Keep Docker container after run for debugging")
	flag.StringVar(&f.ModelProvider, "provider", "", "Model provider: openai-compatible, anthropic, replay, or human")
	flag.StringVar(&f.BaseURL, "base-url", "", "Provider base URL")
	flag.StringVar(&f.ModelName, "model", "", "Model name")
	flag.StringVar(&f.APIKeyEnv, "api-key-env", "", "Environment variable containing API key")
	flag.StringVar(&f.ReplayFile, "replay-file", "", "Replay responses file for --provider replay")
	flag.IntVar(&f.MaxSteps, "max-steps", 0, "Maximum agent steps")
	flag.DurationVar(&f.CommandTimeout, "command-timeout", 0, "Timeout per command, e.g. 30s, 2m")
	flag.IntVar(&f.MaxObservationChars, "max-observation-chars", 0, "Maximum observation chars sent back to model")
	flag.Float64Var(&f.Temperature, "temperature", 0, "Sampling temperature")
	flag.IntVar(&f.MaxTokens, "max-tokens", 0, "Maximum output tokens")
	flag.StringVar(&f.SystemPrompt, "system-prompt", "", "Inline system prompt override")
	flag.StringVar(&f.SystemPromptFile, "system-prompt-file", "", "Path to system prompt override")
	flag.BoolVar(&f.PrintTrajectory, "print-trajectory", false, "Print trajectory JSON path at the end")
	flag.BoolVar(&f.PrintPatch, "print-patch", false, "Print patch to stdout at the end")
	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(fl *flag.Flag) { explicit[fl.Name] = true })
	return f, explicit
}

func applyExplicitFlags(cfg *config.Config, f cliFlags, explicit map[string]bool) {
	if explicit["task"] {
		cfg.Task = f.Task
	}
	if explicit["task-file"] {
		cfg.TaskFile = f.TaskFile
	}
	if explicit["workspace"] {
		cfg.Workspace = f.Workspace
	}
	if explicit["out"] {
		cfg.OutputDir = f.OutputDir
	}
	if explicit["env"] {
		cfg.Environment.Type = f.EnvType
	}
	if explicit["image"] {
		cfg.Environment.DockerImage = f.DockerImage
	}
	if explicit["container-name"] {
		cfg.Environment.ContainerName = f.ContainerName
	}
	if explicit["container-workspace"] {
		cfg.Environment.ContainerWorkspace = f.ContainerWorkspace
	}
	if explicit["keep-container"] {
		cfg.Environment.KeepContainer = f.KeepContainer
	}
	if explicit["provider"] {
		cfg.Model.Provider = f.ModelProvider
	}
	if explicit["base-url"] {
		cfg.Model.BaseURL = f.BaseURL
	}
	if explicit["model"] {
		cfg.Model.Model = f.ModelName
	}
	if explicit["api-key-env"] {
		cfg.Model.APIKeyEnv = f.APIKeyEnv
	}
	if explicit["replay-file"] {
		cfg.Model.ReplayFile = f.ReplayFile
	}
	if explicit["max-steps"] {
		cfg.Agent.MaxSteps = f.MaxSteps
	}
	if explicit["command-timeout"] {
		cfg.Agent.CommandTimeout = f.CommandTimeout
	}
	if explicit["max-observation-chars"] {
		cfg.Agent.MaxObservationChars = f.MaxObservationChars
	}
	if explicit["temperature"] {
		cfg.Model.Temperature = f.Temperature
	}
	if explicit["max-tokens"] {
		cfg.Model.MaxTokens = f.MaxTokens
	}
	if explicit["system-prompt"] {
		cfg.Agent.SystemPrompt = f.SystemPrompt
	}
	if explicit["system-prompt-file"] {
		cfg.Agent.SystemPromptFile = f.SystemPromptFile
	}
	if explicit["print-trajectory"] {
		cfg.PrintTrajectory = f.PrintTrajectory
	}
	if explicit["print-patch"] {
		cfg.PrintPatch = f.PrintPatch
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
