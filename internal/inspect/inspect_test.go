package inspect

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/example/go-code-agent/internal/environment"
	"github.com/example/go-code-agent/internal/trajectory"
)

func TestRenderTrajectorySummary(t *testing.T) {
	run := &trajectory.Run{
		Format:    "go-code-agent-trajectory-v1",
		Status:    "submitted",
		Model:     "replay",
		Workspace: "/tmp/ws",
		StartedAt: time.Unix(1, 0),
		EndedAt:   time.Unix(2, 0),
		Task:      "fix bug",
		Stats:     trajectory.Stats{APICalls: 1, DurationSeconds: 1.2},
		Steps: []trajectory.Step{{
			Index:            1,
			AssistantMessage: "I will inspect.\n```bash\nls\n```",
			Command:          "ls",
			Observation:      &environment.ExecResult{Stdout: "file.txt", ExitCode: 0},
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, run, Options{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Status:     submitted", "Steps:", "command=\"ls\"", "file.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRenderStepOutOfRange(t *testing.T) {
	run := &trajectory.Run{Steps: []trajectory.Step{{Index: 1}}}
	var buf bytes.Buffer
	if err := Render(&buf, run, Options{Step: 2}); err == nil {
		t.Fatal("expected step out of range error")
	}
}
