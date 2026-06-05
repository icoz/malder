package tool

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"
)

var ErrTooManyRequests = errors.New("too many requests (429)")

type SearchTool struct {
    endpoint   string
    httpClient *http.Client
}

func NewSearchTool(endpoint string, timeout time.Duration) *SearchTool {
    if timeout == 0 {
        timeout = 10 * time.Second
    }
    return &SearchTool{
        endpoint:   strings.TrimSuffix(endpoint, "/"),
        httpClient: &http.Client{Timeout: timeout},
    }
}

func (t *SearchTool) Name() string { return "search" }

func (t *SearchTool) Description() string {
    return `Ищет информацию в интернете через Яндекс. Возвращает список результатов (заголовок, ссылка, сниппет).
Аргументы:
  - query (строка, обязательный): поисковый запрос.
  - num (целое, необязательный): количество результатов (по умолчанию 5, максимум 10).`
}

type searchResponse struct {
    Results []struct {
        Title   string `json:"title"`
        Link    string `json:"link"`
        Snippet string `json:"snippet"`
    } `json:"results"`
}

func (t *SearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
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

    num := 5
    if numRaw, ok := args["num"]; ok {
        switch v := numRaw.(type) {
        case float64:
            num = int(v)
        case int:
            num = v
        }
        if num < 1 {
            num = 1
        }
        if num > 10 {
            num = 10
        }
    }

    reqURL := fmt.Sprintf("%s/search?q=%s&engine=yandex&num=%d",
        t.endpoint, url.QueryEscape(query), num)

    httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
    if err != nil {
        return "", fmt.Errorf("ошибка создания запроса: %w", err)
    }

    resp, err := t.httpClient.Do(httpReq)
    if err != nil {
        return "", fmt.Errorf("ошибка выполнения поиска: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == 429 {
        return "", ErrTooManyRequests
    }
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("OpenSerp вернул код %d: %s", resp.StatusCode, string(body))
    }

    var sr searchResponse
    if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
        return "", fmt.Errorf("ошибка парсинга JSON: %w", err)
    }
    if len(sr.Results) == 0 {
        return fmt.Sprintf("По запросу '%s' ничего не найдено.", query), nil
    }

    var builder strings.Builder
    builder.WriteString(fmt.Sprintf("Результаты поиска по запросу '%s':\n\n", query))
    for i, res := range sr.Results {
        builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, res.Title))
        builder.WriteString(fmt.Sprintf("   Ссылка: %s\n", res.Link))
        builder.WriteString(fmt.Sprintf("   Краткое описание: %s\n\n", res.Snippet))
    }
    return builder.String(), nil
}
