package swebench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInstancesJSONLAndConvert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.jsonl")
	input := `{"instance_id":"django__django-1","repo":"django/django","base_commit":"abc123","problem_statement":"Fix it","hints_text":"Look at tests"}` + "\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	instances, err := LoadInstances(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("got %d instances", len(instances))
	}
	task := TaskFromInstance(instances[0], ConvertOptions{AppendHints: true, TestCommands: []string{"pytest"}})
	if task.ID != "django__django-1" {
		t.Fatalf("bad id: %s", task.ID)
	}
	if task.Repo != "https://github.com/django/django.git" {
		t.Fatalf("bad repo: %s", task.Repo)
	}
	if !strings.Contains(task.ProblemStatement, "Hints:") {
		t.Fatalf("expected hints in problem statement: %q", task.ProblemStatement)
	}
	if len(task.Test) != 1 || task.Test[0] != "pytest" {
		t.Fatalf("bad tests: %#v", task.Test)
	}
}

func TestWriteTasksAndPredictions(t *testing.T) {
	dir := t.TempDir()
	instances := []Instance{{InstanceID: "repo__proj-1", Repo: "owner/proj", BaseCommit: "abc", ProblemStatement: "Fix"}}
	paths, err := WriteTasks(instances, filepath.Join(dir, "tasks"), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths", len(paths))
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://github.com/owner/proj.git") {
		t.Fatalf("unexpected task JSON: %s", string(data))
	}

	runDir := filepath.Join(dir, "bench", "repo__proj-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"task_id": "repo__proj-1", "status": "submitted"}
	raw, _ := json.Marshal(result)
	if err := os.WriteFile(filepath.Join(runDir, "result.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "patch.diff"), []byte("diff --git a/x b/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preds, err := BuildPredictions(filepath.Join(dir, "bench"), "my-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(preds) != 1 || preds[0].InstanceID != "repo__proj-1" || preds[0].ModelNameOrPath != "my-model" {
		t.Fatalf("bad preds: %#v", preds)
	}
	jsonPath := filepath.Join(dir, "predictions.json")
	if err := SavePredictionsJSON(jsonPath, preds); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "model_patch") {
		t.Fatalf("bad predictions file: %s", string(b))
	}
}
