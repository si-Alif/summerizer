package huggingface

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/si-Alif/summerizer/internal/llm"
)

const (
	defaultModel   = "llama-3.1-8b-instant"
	defaultBaseURL = "https://api.groq.com/"
	requestTimeout = 120 * time.Second
)

type HFModel struct {
	httpClient *http.Client
	model      string
	baseURL    string
	apiKey     string
}

func NewHFModel(baseURL, model string) (*HFModel, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}

	apiKey := strings.TrimSpace(os.Getenv("SUMMERIZER_HF_API_KEY"))
	if apiKey == "" {
		// Optional fallback if you prefer HF docs naming:
		apiKey = strings.TrimSpace(os.Getenv("HF_TOKEN"))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("huggingface: missing HUGGINGFACE_API_KEY (or HF_TOKEN)")
	}

	return &HFModel{
		httpClient: &http.Client{Timeout: requestTimeout},
		model:      model,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
	}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (m *HFModel) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("huggingface: at least one message is required")
	}

	msgs := make([]chatMessage, 0, len(req.Messages))
	for _, in := range req.Messages {
		msgs = append(msgs, chatMessage{
			Role:    in.Role,
			Content: in.Content,
		})
	}

	body, err := json.Marshal(chatRequest{
		Model:       m.model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	})
	if err != nil {
		return nil, fmt.Errorf("huggingface: marshal request: %w", err)
	}

	url := m.baseURL + "/openai/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("huggingface: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)

	httpResp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("huggingface: do request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		errBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("huggingface: unexpected status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var out chatCompletionResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("huggingface: decode response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("huggingface: empty completion content")
	}

	return &llm.GenerateResponse{Text: out.Choices[0].Message.Content}, nil
}
