package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/model"
)

func TestRunnerWithReplayModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := environment.NewLocalEnvironment(dir)
	if err != nil {
		t.Fatal(err)
	}
	client := model.NewReplayClientFromStrings([]string{
		"I will inspect.\n```bash\npwd\n```",
		"Done.\n```bash\necho COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT\n```",
	})
	runner := &Runner{Model: client, Env: env}
	res, err := runner.Run(context.Background(), "test", Options{MaxSteps: 3, CommandTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitted" || res.Steps != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Trajectory.Steps[1].Command, SubmitSentinel) {
		t.Fatalf("missing submit command: %+v", res.Trajectory.Steps[1])
	}
}
