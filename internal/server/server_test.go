package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/go-code-agent/internal/config"
)

func TestServerRunLifecycle(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(tmp, "responses.json")
	responses := []string{
		"I will inspect.\n```bash\ncat hello.txt\n```",
		"Done.\n```bash\necho COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT\n```",
	}
	data, _ := json.Marshal(responses)
	if err := os.WriteFile(replay, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Model.Provider = "replay"
	cfg.Model.ReplayFile = replay
	cfg.Agent.MaxSteps = 4
	cfg.Workspace = ws
	cfg.OutputDir = filepath.Join(tmp, "runs", "base")

	srv, err := New(Options{BaseConfig: cfg, OutputDir: filepath.Join(tmp, "runs"), Workers: 1, QueueSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	bodyData, err := json.Marshal(RunRequest{Task: "read hello and submit", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewReader(bodyData)
	resp, err := http.Post(ts.URL+"/v1/runs", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var created CreateRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("empty id")
	}

	var job Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(ts.URL + "/v1/runs/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			r.Body.Close()
			t.Fatal(err)
		}
		r.Body.Close()
		if job.Status == StatusSucceeded || job.Status == StatusFailed || job.Status == StatusCancelled {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if job.Status != StatusSucceeded {
		t.Fatalf("job status=%s error=%s", job.Status, job.Error)
	}
	if job.Result == nil || job.Result.Steps != 2 {
		t.Fatalf("unexpected result: %+v", job.Result)
	}
	art, err := http.Get(ts.URL + "/v1/runs/" + created.ID + "/trajectory")
	if err != nil {
		t.Fatal(err)
	}
	defer art.Body.Close()
	if art.StatusCode != http.StatusOK {
		t.Fatalf("trajectory status=%d", art.StatusCode)
	}
}
