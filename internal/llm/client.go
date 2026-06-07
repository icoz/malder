package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/icoz/malder/internal/log"
)

type Client struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

type Config struct {
	Endpoint string
	APIKey   string
	Timeout  time.Duration
}

func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{
		endpoint:   cfg.Endpoint,
		apiKey:     cfg.APIKey,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) Complete(ctx context.Context, model string, messages []ChatMessage, temperature float64) (string, error) {
	log.Debug("→ llm.Complete(model=%s, messages=%d, temp=%.2f)", model, len(messages), temperature)

	var totalLen int
	for _, m := range messages {
		totalLen += len(m.Content)
	}
	log.Info("LLM запрос: модель=%s, сообщений=%d, символов=%d, temp=%.2f", model, len(messages), totalLen, temperature)

	reqBody := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      false,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/v1/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if chatResp.Error != nil {
		err := fmt.Errorf("LLM error: %s", chatResp.Error.Message)
		log.Info("LLM ответ: ошибка=%v", err)
		log.Debug("← llm.Complete = (\"\", %v)", err)
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		err := fmt.Errorf("no choices in response")
		log.Info("LLM ответ: ошибка=%v", err)
		log.Debug("← llm.Complete = (\"\", %v)", err)
		return "", err
	}
	result := chatResp.Choices[0].Message.Content
	truncated := result
	if len(truncated) > 500 {
		truncated = truncated[:500] + "..."
	}
	if chatResp.Usage != nil {
		log.Info("LLM ответ: длина=%d, токены: input=%d, output=%d, total=%d",
			len(result), chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens, chatResp.Usage.TotalTokens)
	} else {
		log.Info("LLM ответ: длина=%d, начало: %s", len(result), truncated)
	}
	log.Debug("← llm.Complete = (len=%d, nil)", len(result))
	return result, nil
}

func (c *Client) CompleteSimple(ctx context.Context, model string, systemPrompt, userPrompt string, temperature float64) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return c.Complete(ctx, model, messages, temperature)
}
