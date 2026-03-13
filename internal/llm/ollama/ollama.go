package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/si-Alif/summerizer/internal/llm"
)

const (
	defaultModel   = "phi3:latest"
	defaultBaseURL = "http://localhost:11434"
	requestTimeout = 1200 * time.Second
)

type OllamaModel struct {
	httpClient *http.Client
	model      string
	baseURL    string
}

func NewOllamaModel(baseURL, model string) *OllamaModel {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultModel
	}

	return &OllamaModel{
		httpClient: &http.Client{Timeout: requestTimeout},
		model:      model,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature float32 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  chatOptions   `json:"options,omitempty"`
}

type chatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

func (o *OllamaModel) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("ollama: at least one message is required")
	}

	messages := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, chatMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	payload, err := json.Marshal(chatRequest{
		Model:    o.model,
		Messages: messages,
		Stream:   false,
		Options: chatOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	url := o.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var decoded chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("ollama: api error: %s", decoded.Error)
	}
	if strings.TrimSpace(decoded.Message.Content) == "" {
		return nil, fmt.Errorf("ollama: empty response content")
	}

	return &llm.GenerateResponse{Text: decoded.Message.Content}, nil
}
