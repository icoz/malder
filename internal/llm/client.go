package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/icoz/malder/internal/log"
)

type Client struct {
	endpoint      string
	apiKey        string
	timeout       time.Duration
	httpClient    *http.Client
	retryAttempts int
	retryDelay    time.Duration
}

type Config struct {
	Endpoint        string
	APIKey          string
	Timeout         time.Duration
	RetryMaxAttempts int
	RetryBaseDelay  time.Duration
}

func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.RetryMaxAttempts <= 0 {
		cfg.RetryMaxAttempts = 3
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 1 * time.Second
	}
	return &Client{
		endpoint:      cfg.Endpoint,
		apiKey:        cfg.APIKey,
		timeout:       cfg.Timeout,
		httpClient:    &http.Client{},
		retryAttempts: cfg.RetryMaxAttempts,
		retryDelay:    cfg.RetryBaseDelay,
	}
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
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

// jitter returns a duration in [d*0.5, d*1.5).
func jitter(d time.Duration) time.Duration {
	half := int64(d) / 2
	n, err := rand.Int(rand.Reader, big.NewInt(half))
	if err != nil {
		return d
	}
	return time.Duration(int64(d) - half + n.Int64())
}

func (c *Client) Complete(ctx context.Context, model string, messages []ChatMessage, temperature float64, maxTokens int) (string, error) {
	reqID := fmt.Sprintf("%x", time.Now().UnixNano())[:12]
	log.Debug("→ llm.Complete(req_id=%s, model=%s, messages=%d, temp=%.2f, maxTokens=%d)", reqID, model, len(messages), temperature, maxTokens)

	var totalLen int
	for _, m := range messages {
		switch c := m.Content.(type) {
		case string:
			totalLen += len(c)
		default:
		}
	}
	log.Info("LLM запрос: req_id=%s, модель=%s, сообщений=%d, символов=%d, temp=%.2f, maxTokens=%d", reqID, model, len(messages), totalLen, temperature, maxTokens)

	reqBody := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      false,
	}
	if maxTokens > 0 {
		reqBody.MaxTokens = &maxTokens
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	var httpStatus int
	var lastBody string
	start := time.Now()

	for attempt := 0; attempt < c.retryAttempts; attempt++ {
		if attempt > 0 {
			var delay time.Duration
			if isDeadlineErr(lastErr) {
				delay = jitter(c.retryDelay)
			} else {
				delay = jitter(c.retryDelay * time.Duration(1<<(attempt-1)))
			}
			log.Warn("LLM request failed (attempt %d/%d): req_id=%s, model=%s, chars=%d, err=%v, retrying in %v", attempt+1, c.retryAttempts, reqID, model, totalLen, lastErr, delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)

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
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("http do: %w", err)
			httpStatus = 0
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("read body: %w", err)
			httpStatus = resp.StatusCode
			lastBody = string(body)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
			httpStatus = resp.StatusCode
			lastBody = string(body)
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
			log.Debug("← llm.Complete(req_id=%s) = (\"\", %v)", reqID, err)
			return "", err
		}
		if len(chatResp.Choices) == 0 {
			err := fmt.Errorf("no choices in response")
			log.Info("LLM ответ: ошибка=%v", err)
			log.Debug("← llm.Complete(req_id=%s) = (\"\", %v)", reqID, err)
			return "", err
		}
		resultAny := chatResp.Choices[0].Message.Content
		result, _ := resultAny.(string)
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
		log.Debug("← llm.Complete(req_id=%s) = (len=%d, nil)", reqID, len(result))
		return result, nil
	}

	if lastErr != nil {
		if isDeadlineErr(lastErr) {
			lastErr = fmt.Errorf("timeout after %s: %w", c.timeout, lastErr)
		} else if errors.Is(lastErr, context.Canceled) {
			lastErr = fmt.Errorf("client disconnected: %w", lastErr)
		}
	}

	snippet := lastBody
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	log.Warn("LLM request failed: req_id=%s, model=%s, messages=%d, chars=%d, attempts=%d/%d, duration=%v, last_err=%v, http_status=%d, body_snippet=%q",
		reqID, model, len(messages), totalLen, c.retryAttempts, c.retryAttempts, time.Since(start), lastErr, httpStatus, snippet)
	return "", fmt.Errorf("LLM request failed after %d attempts: %w", c.retryAttempts, lastErr)
}

// isDeadlineErr returns true if err is a context deadline exceeded or a timeout.
func isDeadlineErr(err error) bool {
	if err == nil {
		return false
	}
	if err == context.DeadlineExceeded {
		return true
	}
	// http.Client wraps deadline errors for Do requests.
	if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() {
		return true
	}
	return false
}

func (c *Client) CompleteSimple(ctx context.Context, model string, systemPrompt, userPrompt string, temperature float64, maxTokens int) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return c.Complete(ctx, model, messages, temperature, maxTokens)
}

type VisionContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

func (c *Client) CompleteVision(ctx context.Context, model, systemPrompt, userText string, base64Images []string, temperature float64, maxTokens int) (string, error) {
	content := make([]VisionContentPart, 0, 1+len(base64Images))
	content = append(content, VisionContentPart{Type: "text", Text: userText})
	for _, b64 := range base64Images {
		content = append(content, VisionContentPart{
			Type: "image_url",
			ImageURL: &struct {
				URL string `json:"url"`
			}{URL: "data:image/jpeg;base64," + b64},
		})
	}
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: content},
	}
	return c.Complete(ctx, model, messages, temperature, maxTokens)
}
