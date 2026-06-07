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
	timeout    time.Duration
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
		timeout:    cfg.Timeout,
		httpClient: &http.Client{},
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

	const maxAttempts = 3
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := c.timeout * time.Duration(1<<(attempt-1))
			log.Warn("LLM request failed (attempt %d/%d): %v, retrying in %v", attempt, maxAttempts-1, lastErr, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		attemptTimeout := c.timeout * time.Duration(1<<(attempt))
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)

		httpReq, err := http.NewRequestWithContext(attemptCtx, "POST", c.endpoint+"/v1/chat/completions", bytes.NewReader(jsonData))
		if err != nil {
			cancel()
			return "", fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(httpReq)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("http do: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read body: %w", err)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
			continue
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

	return "", fmt.Errorf("LLM request failed after %d attempts: %w", maxAttempts, lastErr)
}

func (c *Client) CompleteSimple(ctx context.Context, model string, systemPrompt, userPrompt string, temperature float64) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return c.Complete(ctx, model, messages, temperature)
}
