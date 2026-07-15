package model

import "context"

// Message is a chat message exchanged with the model.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Options controls a single model generation call.
type Options struct {
	Temperature float64
	MaxTokens   int
}

// Usage contains token accounting returned by a provider when available.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// Response is the normalized model output.
type Response struct {
	Content string `json:"content"`
	Usage   Usage  `json:"usage,omitempty"`
	Raw     any    `json:"raw,omitempty"`
}

// Client is the common interface for LLM adapters.
type Client interface {
	Generate(ctx context.Context, messages []Message, opts Options) (*Response, error)
	Name() string
}
