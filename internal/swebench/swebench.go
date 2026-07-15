package swebench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/example/go-code-agent/internal/benchmark"
	"github.com/example/go-code-agent/internal/config"
)

// Instance is the small subset of SWE-bench-style dataset fields needed for inference.
// Unknown fields are intentionally ignored so this can read SWE-bench, SWE-bench Lite,
// SWE-bench Verified, and similar JSON/JSONL exports.
type Instance struct {
	InstanceID             string          `json:"instance_id"`
	Repo                   string          `json:"repo"`
	BaseCommit             string          `json:"base_commit"`
	ProblemStatement       string          `json:"problem_statement"`
	HintsText              string          `json:"hints_text"`
	CreatedAt              string          `json:"created_at"`
	Version                string          `json:"version"`
	FAILToPASS             json.RawMessage `json:"FAIL_TO_PASS"`
	PASSToPASS             json.RawMessage `json:"PASS_TO_PASS"`
	TestPatch              string          `json:"test_patch"`
	Patch                  string          `json:"patch"`
	EnvironmentSetupCommit string          `json:"environment_setup_commit"`
}

// Prediction is compatible with common SWE-bench prediction files.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

// ConvertOptions controls dataset-to-task conversion.
type ConvertOptions struct {
	ConfigFile     string
	AppendHints    bool
	TestCommands   []string
	DefaultRepoURL string
}

// LoadInstances reads JSONL, JSON array, or dictionary-shaped JSON files.
func LoadInstances(path string) ([]Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 {
		return nil, fmt.Errorf("empty SWE-bench input %s", path)
	}
	if trim[0] == '[' {
		var instances []Instance
		if err := json.Unmarshal(trim, &instances); err != nil {
			return nil, err
		}
		return normalizeInstances(instances)
	}
	if trim[0] == '{' {
		var dict map[string]Instance
		if err := json.Unmarshal(trim, &dict); err == nil && len(dict) > 0 {
			instances := make([]Instance, 0, len(dict))
			for id, inst := range dict {
				if inst.InstanceID == "" {
					inst.InstanceID = id
				}
				instances = append(instances, inst)
			}
			return normalizeInstances(instances)
		}
		var single Instance
		if err := json.Unmarshal(trim, &single); err != nil {
			return nil, err
		}
		return normalizeInstances([]Instance{single})
	}

	s := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 1024*1024)
	s.Buffer(buf, 64*1024*1024)
	var instances []Instance
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		instances = append(instances, inst)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return normalizeInstances(instances)
}

func normalizeInstances(instances []Instance) ([]Instance, error) {
	seen := map[string]bool{}
	for i := range instances {
		instances[i].InstanceID = strings.TrimSpace(instances[i].InstanceID)
		if instances[i].InstanceID == "" {
			return nil, fmt.Errorf("instance %d: missing instance_id", i+1)
		}
		if seen[instances[i].InstanceID] {
			return nil, fmt.Errorf("duplicate instance_id %s", instances[i].InstanceID)
		}
		seen[instances[i].InstanceID] = true
		if strings.TrimSpace(instances[i].Repo) == "" {
			return nil, fmt.Errorf("instance %s: missing repo", instances[i].InstanceID)
		}
		if strings.TrimSpace(instances[i].BaseCommit) == "" {
			return nil, fmt.Errorf("instance %s: missing base_commit", instances[i].InstanceID)
		}
		if strings.TrimSpace(instances[i].ProblemStatement) == "" {
			return nil, fmt.Errorf("instance %s: missing problem_statement", instances[i].InstanceID)
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].InstanceID < instances[j].InstanceID })
	return instances, nil
}

// TaskFromInstance converts one SWE-bench instance to the internal benchmark task format.
func TaskFromInstance(inst Instance, opts ConvertOptions) benchmark.TaskSpec {
	problem := strings.TrimSpace(inst.ProblemStatement)
	if opts.AppendHints && strings.TrimSpace(inst.HintsText) != "" {
		problem += "\n\nHints:\n" + strings.TrimSpace(inst.HintsText)
	}
	return benchmark.TaskSpec{
		ID:               inst.InstanceID,
		Repo:             normalizeRepo(inst.Repo, opts.DefaultRepoURL),
		BaseCommit:       inst.BaseCommit,
		ProblemStatement: problem,
		Test:             append([]string(nil), opts.TestCommands...),
		ConfigFile:       opts.ConfigFile,
	}
}

func normalizeRepo(repo string, defaultRepoURL string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	if strings.Contains(repo, "://") || strings.HasPrefix(repo, "git@") || strings.HasSuffix(repo, ".git") {
		return repo
	}
	if strings.Count(repo, "/") == 1 {
		base := strings.TrimRight(defaultRepoURL, "/")
		if base == "" {
			base = "https://github.com"
		}
		return base + "/" + repo + ".git"
	}
	return repo
}

// WriteTasks converts instances to task JSON files and returns their paths.
func WriteTasks(instances []Instance, outDir string, opts ConvertOptions) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(instances))
	for _, inst := range instances {
		task := TaskFromInstance(inst, opts)
		path := filepath.Join(outDir, safeFileName(inst.InstanceID)+".json")
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// Run converts SWE-bench instances to internal tasks and runs them with the benchmark runner.
func Run(ctx context.Context, inputPath string, opts RunOptions) (benchmark.Summary, []Prediction, error) {
	instances, err := LoadInstances(inputPath)
	if err != nil {
		return benchmark.Summary{}, nil, err
	}
	if opts.Limit > 0 && opts.Limit < len(instances) {
		instances = instances[:opts.Limit]
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "runs/swebench"
	}
	tasksDir := opts.TasksDir
	if tasksDir == "" {
		tasksDir = filepath.Join(opts.OutputDir, "_tasks")
	}
	taskPaths, err := WriteTasks(instances, tasksDir, ConvertOptions{
		ConfigFile:     opts.TaskConfigFile,
		AppendHints:    opts.AppendHints,
		TestCommands:   opts.TestCommands,
		DefaultRepoURL: opts.DefaultRepoURL,
	})
	if err != nil {
		return benchmark.Summary{}, nil, err
	}
	summary, err := benchmark.Run(ctx, taskPaths, benchmark.Options{
		BaseConfig:      opts.BaseConfig,
		OutputDir:       opts.OutputDir,
		Concurrency:     opts.Concurrency,
		Limit:           0,
		ContinueOnError: opts.ContinueOnError,
		KeepWorkspaces:  opts.KeepWorkspaces,
	})
	if err != nil {
		return summary, nil, err
	}
	preds, err := BuildPredictions(opts.OutputDir, opts.ModelName)
	if err != nil {
		return summary, nil, err
	}
	return summary, preds, nil
}

// RunOptions controls `codeagent swebench run`.
type RunOptions struct {
	BaseConfig      config.Config
	OutputDir       string
	TasksDir        string
	TaskConfigFile  string
	ModelName       string
	Concurrency     int
	Limit           int
	ContinueOnError bool
	KeepWorkspaces  bool
	AppendHints     bool
	TestCommands    []string
	DefaultRepoURL  string
}

// BuildPredictions scans a benchmark output directory and builds SWE-bench predictions.
func BuildPredictions(benchDir string, modelName string) ([]Prediction, error) {
	entries, err := os.ReadDir(benchDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = "go-code-agent"
	}
	var preds []Prediction
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		runDir := filepath.Join(benchDir, entry.Name())
		resultPath := filepath.Join(runDir, "result.json")
		patchPath := filepath.Join(runDir, "patch.diff")
		data, err := os.ReadFile(resultPath)
		if err != nil {
			continue
		}
		var res benchmark.Result
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, fmt.Errorf("read %s: %w", resultPath, err)
		}
		patch, err := os.ReadFile(patchPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if res.TaskID == "" {
			res.TaskID = entry.Name()
		}
		preds = append(preds, Prediction{InstanceID: res.TaskID, ModelNameOrPath: modelName, ModelPatch: string(patch)})
	}
	sort.Slice(preds, func(i, j int) bool { return preds[i].InstanceID < preds[j].InstanceID })
	return preds, nil
}

// SavePredictionsJSON writes the dictionary format accepted by SWE-bench tooling.
func SavePredictionsJSON(path string, preds []Prediction) error {
	out := map[string]Prediction{}
	for _, p := range preds {
		out[p.InstanceID] = p
	}
	return writeJSON(path, out)
}

// SavePredictionsJSONL writes one prediction object per line for streaming workflows.
func SavePredictionsJSONL(path string, preds []Prediction) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, p := range preds {
		if err := enc.Encode(p); err != nil {
			return err
		}
	}
	return nil
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

func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "instance"
	}
	return out
}
