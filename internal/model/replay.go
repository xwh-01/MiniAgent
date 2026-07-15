package model

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReplayClient returns canned assistant responses. It is useful for deterministic
// tests, demos, and reproducing parser/environment behavior without an API key.
type ReplayClient struct {
	NameValue string
	responses []Response
	index     int
}

func NewReplayClient(path string) (*ReplayClient, error) {
	responses, err := LoadReplayResponses(path)
	if err != nil {
		return nil, err
	}
	return &ReplayClient{NameValue: "replay:" + filepath.Base(path), responses: responses}, nil
}

func NewReplayClientFromStrings(items []string) *ReplayClient {
	responses := make([]Response, 0, len(items))
	for _, item := range items {
		responses = append(responses, Response{Content: item})
	}
	return &ReplayClient{NameValue: "replay:inline", responses: responses}
}

func (c *ReplayClient) Name() string {
	if c.NameValue != "" {
		return c.NameValue
	}
	return "replay"
}

func (c *ReplayClient) Generate(ctx context.Context, messages []Message, opts Options) (*Response, error) {
	_ = ctx
	_ = messages
	_ = opts
	if c.index >= len(c.responses) {
		return nil, fmt.Errorf("replay exhausted after %d responses", len(c.responses))
	}
	resp := c.responses[c.index]
	c.index++
	return &resp, nil
}

func LoadReplayResponses(path string) ([]Response, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("replay file is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trim := strings.TrimSpace(string(data))
	if trim == "" {
		return nil, errors.New("replay file is empty")
	}
	if strings.HasPrefix(trim, "[") {
		var stringsOnly []string
		if err := json.Unmarshal(data, &stringsOnly); err == nil {
			out := make([]Response, 0, len(stringsOnly))
			for _, s := range stringsOnly {
				out = append(out, Response{Content: s})
			}
			return out, nil
		}
		var responses []Response
		if err := json.Unmarshal(data, &responses); err != nil {
			return nil, err
		}
		return responses, nil
	}

	// JSONL mode: each line can be {"content":"..."} or a raw line.
	var out []Response
	s := bufio.NewScanner(strings.NewReader(string(data)))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var resp Response
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				return nil, err
			}
			out = append(out, resp)
			continue
		}
		out = append(out, Response{Content: line})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("replay file contains no responses")
	}
	return out, nil
}
