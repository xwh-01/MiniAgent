package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/go-code-agent/internal/benchmark"
)

func TestBuildAndRenderReport(t *testing.T) {
	dir := t.TempDir()
	sum := benchmark.Summary{
		StartedAt: time.Unix(1, 0),
		EndedAt:   time.Unix(3, 0),
		Total:     2,
		Resolved:  1,
		Failed:    1,
		Results: []benchmark.Result{
			{TaskID: "ok", Status: "submitted", Resolved: true, TestPassed: true, Steps: 3, DurationSeconds: 10, TrajectoryPath: "ok/trajectory.json"},
			{TaskID: "bad", Status: "tests_failed", Resolved: false, TestPassed: false, Steps: 5, DurationSeconds: 20, Error: "tests failed"},
		},
	}
	data, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ResolveRate != 0.5 || rep.AverageSteps != 4 {
		t.Fatalf("unexpected aggregates: %+v", rep)
	}
	var buf bytes.Buffer
	if err := RenderText(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Rate:      50.0%") {
		t.Fatalf("unexpected text report:\n%s", buf.String())
	}
	jsonPath := filepath.Join(dir, "report.json")
	htmlPath := filepath.Join(dir, "report.html")
	if err := SaveJSON(jsonPath, rep); err != nil {
		t.Fatal(err)
	}
	if err := SaveHTML(htmlPath, rep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "CodeAgent Benchmark Report") {
		t.Fatalf("unexpected html: %s", string(html))
	}
}
