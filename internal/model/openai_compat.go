package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatClient talks to OpenAI-compatible chat completions endpoints.
type OpenAICompatClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func NewOpenAICompatClient(baseURL, apiKey, modelName string) *OpenAICompatClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAICompatClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *OpenAICompatClient) Name() string {
	return "openai-compatible:" + c.Model
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	ID      string `json:"id,omitempty"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	Model   string `json:"model,omitempty"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Code    any    `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

func (c *OpenAICompatClient) Generate(ctx context.Context, messages []Message, opts Options) (*Response, error) {
	if c.BaseURL == "" {
		return nil, errors.New("base URL is empty")
	}
	if c.Model == "" {
		return nil, errors.New("model is empty")
	}
	if c.APIKey == "" {
		return nil, errors.New("API key is empty")
	}

	payload := chatRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat completion request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode response status=%d body=%s: %w", resp.StatusCode, string(respBody), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("provider error status=%d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return nil, fmt.Errorf("provider error status=%d body=%s", resp.StatusCode, string(respBody))
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("provider returned no choices")
	}

	return &Response{
		Content: parsed.Choices[0].Message.Content,
		Usage:   parsed.Usage,
		Raw:     parsed,
	}, nil
}
