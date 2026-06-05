package tool

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type FetchPageTool struct {
	httpClient *http.Client
}

func NewFetchPageTool(timeout time.Duration) *FetchPageTool {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &FetchPageTool{
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (t *FetchPageTool) Name() string {
	return "fetch_page"
}

func (t *FetchPageTool) Description() string {
	return `Загружает веб-страницу по URL и возвращает её текстовое содержимое (без HTML-тегов).
Аргументы:
  - url (строка, обязательный): полный URL страницы, например "https://example.com/article"
Возвращает текст страницы или сообщение об ошибке.`
}

func (t *FetchPageTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	urlRaw, ok := args["url"]
	if !ok {
		return "", fmt.Errorf("отсутствует обязательный аргумент 'url'")
	}
	url, ok := urlRaw.(string)
	if !ok {
		return "", fmt.Errorf("аргумент 'url' должен быть строкой")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("ошибка создания запроса: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MalderBot/1.0)")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ошибка загрузки страницы: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP ошибка: %d %s", resp.StatusCode, resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ошибка парсинга HTML: %w", err)
	}

	doc.Find("script, style, noscript, meta, link, [onclick], [onload]").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}
	text := body.Text()
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)

	const maxLen = 10000
	if len(text) > maxLen {
		text = text[:maxLen] + "... (текст обрезан)"
	}
	if text == "" {
		return "", fmt.Errorf("не удалось извлечь текст со страницы %s", url)
	}
	return text, nil
}
