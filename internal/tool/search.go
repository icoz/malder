package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/icoz/malder/internal/log"
)

var ErrTooManyRequests = errors.New("too many requests (429)")

type SearchTool struct {
	endpoint   string
	engine     string
	httpClient *http.Client
}

func NewSearchTool(endpoint string, timeout time.Duration, engine string) *SearchTool {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if engine == "" {
		engine = "yandex"
	}
	return &SearchTool{
		endpoint:   strings.TrimSuffix(endpoint, "/"),
		engine:     engine,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (t *SearchTool) Name() string { return "search" }

func (t *SearchTool) Description() string {
	return `Ищет информацию в интернете. Возвращает список результатов (заголовок, ссылка, сниппет).
Аргументы:
  - query (строка, обязательный): поисковый запрос.
  - limit (целое, необязательный): количество результатов (по умолчанию 10, максимум 100).`
}

func (t *SearchTool) Execute(ctx context.Context, args map[string]any) (result string, err error) {
	defer func() {
		if err != nil {
			log.Debug("← SearchTool.Execute = (\"\", %v)", err)
		} else {
			log.Debug("← SearchTool.Execute = (len=%d, nil)", len(result))
		}
	}()
	log.Debug("→ SearchTool.Execute(args=%v)", args)
	queryRaw, ok := args["query"]
	if !ok {
		return "", fmt.Errorf("отсутствует обязательный аргумент 'query'")
	}
	query, ok := queryRaw.(string)
	if !ok {
		return "", fmt.Errorf("аргумент 'query' должен быть строкой")
	}
	if query == "" {
		return "", fmt.Errorf("поисковый запрос не может быть пустым")
	}

	limit := 10
	if limitRaw, ok := args["limit"]; ok {
		switch v := limitRaw.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	engine := t.engine
	if e, ok := args["engine"].(string); ok && e != "" {
		engine = e
	}

	reqURL := fmt.Sprintf("%s/%s/search?text=%s&limit=%d&format=markdown",
		t.endpoint, url.PathEscape(engine), url.QueryEscape(query), limit)

	reqStart := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("ошибка создания запроса: %w", err)
	}

	resp, err := t.httpClient.Do(httpReq)
	reqDur := time.Since(reqStart)
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения поиска: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Info("HTTP search %s: status=%d, duration=%v, body=%d bytes", query, resp.StatusCode, reqDur, len(bodyBytes))

	if resp.StatusCode == 429 {
		return "", ErrTooManyRequests
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenSerp вернул код %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return string(bodyBytes), nil
}
