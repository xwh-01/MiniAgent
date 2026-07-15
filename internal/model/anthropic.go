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

// AnthropicClient talks to Anthropic's Messages API.
type AnthropicClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func NewAnthropicClient(baseURL, apiKey, modelName string) *AnthropicClient {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &AnthropicClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      modelName,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *AnthropicClient) Name() string { return "anthropic:" + c.Model }

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type,omitempty"`
	Role    string `json:"role,omitempty"`
	Model   string `json:"model,omitempty"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
	Usage struct {
		InputTokens  int `json:"input_tokens,omitempty"`
		OutputTokens int `json:"output_tokens,omitempty"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

func (c *AnthropicClient) Generate(ctx context.Context, messages []Message, opts Options) (*Response, error) {
	if c.BaseURL == "" {
		return nil, errors.New("base URL is empty")
	}
	if c.Model == "" {
		return nil, errors.New("model is empty")
	}
	if c.APIKey == "" {
		return nil, errors.New("API key is empty")
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	system, anthMessages := splitAnthropicMessages(messages)
	payload := anthropicRequest{
		Model:       c.Model,
		MaxTokens:   maxTokens,
		Temperature: opts.Temperature,
		System:      system,
		Messages:    anthMessages,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode response status=%d body=%s: %w", resp.StatusCode, string(respBody), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("provider error status=%d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return nil, fmt.Errorf("provider error status=%d body=%s", resp.StatusCode, string(respBody))
	}
	var text strings.Builder
	for _, item := range parsed.Content {
		if item.Type == "text" && item.Text != "" {
			text.WriteString(item.Text)
		}
	}
	if text.Len() == 0 {
		return nil, errors.New("provider returned no text content")
	}
	usage := Usage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
	}
	return &Response{Content: text.String(), Usage: usage, Raw: parsed}, nil
}

func splitAnthropicMessages(messages []Message) (string, []anthropicMessage) {
	var systemParts []string
	var out []anthropicMessage
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "assistant":
			out = append(out, anthropicMessage{Role: "assistant", Content: msg.Content})
		default:
			out = append(out, anthropicMessage{Role: "user", Content: msg.Content})
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}
