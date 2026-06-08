package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/go-shiori/go-readability"
	"github.com/icoz/malder/internal/log"
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
	return `Загружает веб-страницу по URL и возвращает её содержимое в формате Markdown (без рекламы, меню и прочего мусора).
Аргументы:
  - url (строка, обязательный): полный URL страницы, например "https://example.com/article"
Возвращает markdown-текст статьи или сообщение об ошибке.`
}

func (t *FetchPageTool) Execute(ctx context.Context, args map[string]any) (result string, err error) {
	defer func() {
		if err != nil {
			log.Debug("← FetchPageTool.Execute = (\"\", %v)", err)
		} else {
			log.Debug("← FetchPageTool.Execute = (len=%d, nil)", len(result))
		}
	}()
	log.Debug("→ FetchPageTool.Execute(args=%v)", args)
	urlRaw, ok := args["url"]
	if !ok {
		return "", fmt.Errorf("отсутствует обязательный аргумент 'url'")
	}
	pageURL, ok := urlRaw.(string)
	if !ok {
		return "", fmt.Errorf("аргумент 'url' должен быть строкой")
	}

	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("невалидный URL: %w", err)
	}

	reqStart := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("ошибка создания запроса: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MalderBot/1.0)")

	resp, err := t.httpClient.Do(req)
	reqDur := time.Since(reqStart)
	if err != nil {
		return "", fmt.Errorf("ошибка загрузки страницы: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Info("HTTP fetch %s: status=%d, duration=%v", pageURL, resp.StatusCode, reqDur)
		return "", fmt.Errorf("HTTP ошибка: %d %s", resp.StatusCode, resp.Status)
	}

	article, err := readability.FromReader(resp.Body, parsedURL)
	if err != nil {
		return "", fmt.Errorf("ошибка извлечения контента: %w", err)
	}

	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(article.Content)
	if err != nil {
		return "", fmt.Errorf("ошибка конвертации в markdown: %w", err)
	}

	var b strings.Builder
	if article.Title != "" {
		b.WriteString(fmt.Sprintf("# %s\n\n", article.Title))
	}
	b.WriteString(strings.TrimSpace(markdown))

	text := b.String()
	const maxLen = 10000
	if len(text) > maxLen {
		text = text[:maxLen] + "... (текст обрезан)"
	}
	log.Info("HTTP fetch %s: status=200, duration=%v, text=%d chars", pageURL, reqDur, len(text))
	if text == "" {
		return "", fmt.Errorf("не удалось извлечь текст со страницы %s", pageURL)
	}
	return text, nil
}
