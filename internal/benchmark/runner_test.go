package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/example/go-code-agent/internal/config"
)

func TestRunBenchmarkWithReplay(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	workspace := filepath.Join(dir, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workspace, "init")
	runGit(t, workspace, "add", "hello.txt")
	runGit(t, workspace, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	replay := filepath.Join(dir, "responses.json")
	fixCommand := "printf fixed > hello.txt"
	testCommand := "grep -q fixed hello.txt"
	if runtime.GOOS == "windows" {
		fixCommand = `[IO.File]::WriteAllText('hello.txt', "fixed` + "`n" + `")`
		testCommand = `if (-not (Select-String -Quiet -SimpleMatch fixed hello.txt)) { exit 1 }`
	}
	replayData, err := json.Marshal([]string{
		"```bash\n" + fixCommand + "\n```",
		"```bash\necho COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT\n```",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replay, replayData, 0o644); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(dir, "task.yaml")
	taskYAML := "id: simple-fix\nworkspace: " + workspace + "\nproblem_statement: Fix hello.txt\ntest:\n  - " + testCommand + "\n"
	if err := os.WriteFile(taskFile, []byte(taskYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Model.Provider = "replay"
	cfg.Model.ReplayFile = replay
	cfg.Environment.Type = "local"
	cfg.Agent.MaxSteps = 4
	cfg.Agent.CommandTimeout = 5 * time.Second
	out := filepath.Join(dir, "runs")
	summary, err := Run(context.Background(), []string{taskFile}, Options{BaseConfig: cfg, OutputDir: out, Concurrency: 1, KeepWorkspaces: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Resolved != 1 || summary.Errored != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	patch, err := os.ReadFile(filepath.Join(out, "simple-fix", "patch.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "fixed") {
		t.Fatalf("patch does not contain fix:\n%s", string(patch))
	}
	if _, err := os.Stat(filepath.Join(out, "summary.json")); err != nil {
		t.Fatalf("summary not written: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}
