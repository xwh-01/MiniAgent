package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayClientJSONStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replay.json")
	if err := os.WriteFile(path, []byte("[\"```bash\\necho hi\\n```\", \"```bash\\necho done\\n```\"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := NewReplayClient(path)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Generate(context.Background(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content == "" {
		t.Fatal("empty replay response")
	}
	_, err = client.Generate(context.Background(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), nil, Options{})
	if err == nil {
		t.Fatal("expected exhausted replay error")
	}
}
