package gemini

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/si-Alif/summerizer/internal/llm"
)

const (
	defaultModel      = "gemini-3-flash-preview"
	clientInitTimeout = 10 * time.Second
)

type GeminiModel struct {
	client *genai.Client
	model  string
}

func NewGeminiModel(model string) (*GeminiModel, error) {
	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = strings.TrimSpace(os.Getenv("SUMMERIZER_GEMINI_MODEL"))
	}
	if resolvedModel == "" {
		resolvedModel = defaultModel
	}

	apiKey := firstNonEmpty(
		os.Getenv("SUMMERIZER_GEMINI_API_KEY"),
		os.Getenv("GEMINI_API_KEY"),
		os.Getenv("GOOGLE_API_KEY"),
		// Backward-compatible fallback for existing local setup.
		os.Getenv("SUMMERIZER_HF_API_KEY"),
	)
	if apiKey == "" {
		return nil, fmt.Errorf("gemini: missing API key; set SUMMERIZER_GEMINI_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY")
	}

	initCtx, cancel := context.WithTimeout(context.Background(), clientInitTimeout)
	defer cancel()

	client, err := genai.NewClient(initCtx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create client: %w", err)
	}

	return &GeminiModel{
		client: client,
		model:  resolvedModel,
	}, nil
}

func (m *GeminiModel) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("gemini: at least one message is required")
	}
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("gemini: model client is not initialized")
	}

	prompt := flattenMessages(req.Messages)
	config := &genai.GenerateContentConfig{}
	if req.Temperature > 0 {
		temp := req.Temperature
		config.Temperature = &temp
	}
	if req.MaxTokens > 0 {
		config.MaxOutputTokens = int32(req.MaxTokens)
	}

	resp, err := m.client.Models.GenerateContent(ctx, m.model, genai.Text(prompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini: generate content: %w", err)
	}

	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return nil, fmt.Errorf("gemini: empty completion content")
	}

	return &llm.GenerateResponse{Text: text}, nil
}

func flattenMessages(messages []llm.Message) string {
	var b strings.Builder

	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n\n")
		}

		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}

		b.WriteString(strings.ToUpper(role))
		b.WriteString(":\n")
		b.WriteString(msg.Content)
	}

	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}
