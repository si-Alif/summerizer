package llm

import "context"

type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

type GenerateRequest struct {
	Messages    []Message
	Temperature float32
	MaxTokens   int
}

type GenerateResponse struct {
	Text string
}

type Client interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}
