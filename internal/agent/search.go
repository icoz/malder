package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/scheduler"
	"github.com/icoz/malder/internal/tool"
)

type SearchAgent struct {
	searchTool        *tool.SearchTool
	fetchTool         *tool.FetchPageTool
	memory            *memory.LongTermMemory
	scheduler         *scheduler.AdaptiveScheduler
	sourceStore       *memory.SourceStore
	summarizer        *llm.Client
	summarizerModel   string
	maxPagesPerQuery  int
	minRelevantFacts  int
	distanceThreshold float64
	useLLMCheck       bool
	verbosity         VerbosityLevel
}

func NewSearchAgent(
	searchTool *tool.SearchTool,
	fetchTool *tool.FetchPageTool,
	mem *memory.LongTermMemory,
	sched *scheduler.AdaptiveScheduler,
	sourceStore *memory.SourceStore,
	summarizer *llm.Client,
	summarizerModel string,
	maxPagesPerQuery int,
	minRelevantFacts int,
	distanceThreshold float64,
	useLLMCheck bool,
	verbosity VerbosityLevel,
) *SearchAgent {
	log.Debug("→ NewSearchAgent(maxPages=%d, minRelevant=%d, distThreshold=%.3f, llmCheck=%t, verbosity=%s)", maxPagesPerQuery, minRelevantFacts, distanceThreshold, useLLMCheck, verbosity)
	if maxPagesPerQuery <= 0 {
		maxPagesPerQuery = 3
	}
	if minRelevantFacts <= 0 {
		minRelevantFacts = 10
	}
	if distanceThreshold <= 0 {
		distanceThreshold = 0.5
	}
	return &SearchAgent{
		searchTool:        searchTool,
		fetchTool:         fetchTool,
		memory:            mem,
		scheduler:         sched,
		sourceStore:       sourceStore,
		summarizer:        summarizer,
		summarizerModel:   summarizerModel,
		maxPagesPerQuery:  maxPagesPerQuery,
		minRelevantFacts:  minRelevantFacts,
		distanceThreshold: distanceThreshold,
		useLLMCheck:       useLLMCheck,
		verbosity:         verbosity,
	}
}

func (s *SearchAgent) Run(ctx context.Context, queries []string) (err error) {
	defer func() {
		log.Debug("← SearchAgent.Run = %v", err)
	}()
	log.Debug("→ SearchAgent.Run(queries=%v)", queries)
	if len(queries) == 0 {
		return nil
	}
	currentLimit := s.scheduler.GetMaxConcurrent()
	log.Info("SearchAgent: запуск с параллелизмом %d, запросов: %d", currentLimit, len(queries))
	sem := make(chan struct{}, currentLimit)
	var wg sync.WaitGroup
	errCh := make(chan error, len(queries))

	for _, q := range queries {
		wg.Add(1)
		go func(query string) {
			defer log.Recover("SearchAgent.Run.query." + query)
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := s.processQuery(ctx, query); err != nil {
				errCh <- fmt.Errorf("query '%s': %w", query, err)
			}
		}(q)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("ошибки поиска: %v", errs)
	}
	return nil
}

func (s *SearchAgent) processQuery(ctx context.Context, query string) (err error) {
	defer func() {
		log.Debug("← SearchAgent.processQuery(%s) = %v", query, err)
	}()
	log.Debug("→ SearchAgent.processQuery(query=%s)", query)
	if err := s.scheduler.WaitIfNeeded(ctx); err != nil {
		return err
	}
	start := time.Now()
	err = s.processQueryInternal(ctx, query)
	duration := time.Since(start)
	s.scheduler.Record(duration, err)
	return err
}

func (s *SearchAgent) processQueryInternal(ctx context.Context, query string) (err error) {
	defer func() {
		log.Debug("← SearchAgent.processQueryInternal(%s) = %v", query, err)
	}()
	log.Debug("→ SearchAgent.processQueryInternal(query=%s)", query)

	facts, avgDist, recallErr := s.memory.RecallWithTopK(ctx, query, s.memory.TopK())
	if recallErr == nil && len(facts) >= s.minRelevantFacts {
		if avgDist <= s.distanceThreshold {
			if !s.useLLMCheck || s.llmCheck(ctx, query, facts) {
				log.Info("SearchAgent: по запросу '%s' кеш достаточен (%d фактов, средняя дистанция %.3f)", query, len(facts), avgDist)
				return nil
			}
			log.Info("SearchAgent: LLM счёл кеш недостаточным для '%s'", query)
		} else {
			log.Info("SearchAgent: кеш для '%s' недостаточно релевантен (средняя дистанция %.3f > %.3f)", query, avgDist, s.distanceThreshold)
		}
	} else if recallErr == nil {
		log.Info("SearchAgent: по запросу '%s' недостаточно фактов (%d < %d)", query, len(facts), s.minRelevantFacts)
	}
	if recallErr != nil {
		log.Warn("SearchAgent: ошибка поиска в памяти '%s': %v", query, recallErr)
	}

	searchResult, err := s.searchTool.Execute(ctx, map[string]any{"query": query, "limit": s.maxPagesPerQuery + 2})
	if err != nil {
		return fmt.Errorf("поиск не удался: %w", err)
	}

	links := extractLinks(searchResult)
	totalLinks := len(links)
	if totalLinks == 0 {
		log.Info("SearchAgent: для запроса '%s' нет ссылок", query)
		return nil
	}
	if len(links) > s.maxPagesPerQuery {
		links = links[:s.maxPagesPerQuery]
	}
	log.Info("SearchAgent: запрос '%s' — найдено ссылок: %d, обрабатываем: %d", query, totalLinks, len(links))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var loadErrors int
	sem := make(chan struct{}, 2)
	for _, link := range links {
		wg.Add(1)
		go func(url string) {
			defer log.Recover("SearchAgent.processPage." + url)
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := s.processPage(ctx, url, query); err != nil {
				mu.Lock()
				loadErrors++
				mu.Unlock()
				log.Warn("Ошибка загрузки %s: %v", url, err)
			}
		}(link)
	}
	wg.Wait()
	log.Info("SearchAgent: запрос '%s' — загружено: %d, ошибок: %d", query, len(links)-loadErrors, loadErrors)
	return nil
}

func (s *SearchAgent) processPage(ctx context.Context, pageURL, query string) (err error) {
	defer func() {
		log.Debug("← SearchAgent.processPage(%s) = %v", pageURL, err)
	}()
	log.Debug("→ SearchAgent.processPage(url=%s, query=%s)", pageURL, query)

	content, err := s.fetchTool.Execute(ctx, map[string]any{"url": pageURL})
	if err != nil {
		return fmt.Errorf("fetch_page: %w", err)
	}

	// Save raw original
	maxRawLen := s.maxRawLen()
	rawContent := content
	if len(rawContent) > maxRawLen {
		rawContent = rawContent[:maxRawLen] + "... (обрезано)"
	}
	rawKey := fmt.Sprintf("page_raw_%d_%s", time.Now().UnixNano(), hashString(pageURL))
	rawFact := fmt.Sprintf("Источник: %s\nПо запросу: %s\nСодержимое:\n%s", pageURL, query, rawContent)
	if err := s.memory.Save(ctx, rawKey, rawFact); err != nil {
		return fmt.Errorf("сохранение оригинала: %w", err)
	}
	if s.sourceStore != nil {
		s.sourceStore.Put(memory.Provenance{
			Key: rawKey, Kind: "page", SourceURL: pageURL,
			Preview: s.getPreview(rawContent), IsRaw: true,
		})
	}

	// Summarize
	summary, sumErr := s.summarizeContent(ctx, content, pageURL)
	if sumErr != nil {
		log.Warn("SearchAgent: ошибка суммаризации %s: %v", pageURL, sumErr)
		log.Info("SearchAgent: сохранена страница %s (%d символов)", pageURL, len(rawContent))
		return nil
	}

	summaryKey := fmt.Sprintf("page_summary_%d_%s", time.Now().UnixNano(), hashString(pageURL))
	summaryFact := fmt.Sprintf("Источник: %s\nПо запросу: %s\nСуть:\n%s", pageURL, query, summary)
	if err := s.memory.Save(ctx, summaryKey, summaryFact); err != nil {
		return fmt.Errorf("сохранение суммаризации: %w", err)
	}
	if s.sourceStore != nil {
		s.sourceStore.Put(memory.Provenance{
			Key: summaryKey, Kind: "page", SourceURL: pageURL,
			Parents: []string{rawKey}, Preview: s.getPreview(summary), IsRaw: false,
		})
	}

	log.Info("SearchAgent: сохранена страница %s (raw=%d, summary=%d символов)", pageURL, len(rawContent), len(summary))
	return nil
}

func (s *SearchAgent) summarizeContent(ctx context.Context, content, pageURL string) (string, error) {
	const maxLLMInput = 15000
	input := content
	if len(input) > maxLLMInput {
		input = input[:maxLLMInput] + "..."
	}

	factCountGuide := s.factCountGuide()
	prompt := fmt.Sprintf(`Извлеки ключевые факты из текста страницы.

URL: %s

Текст:
%s

%s

Ответ напиши в виде маркированного списка на русском языке.`, pageURL, input, factCountGuide)

	resp, err := s.summarizer.CompleteSimple(ctx, s.summarizerModel, "Ты — экстрактор фактов. Отвечай только фактами, без пояснений.", prompt, 0.3, 0)
	if err != nil {
		return "", err
	}

	maxSummaryLen := s.maxSummaryLen()
	if len(resp) > maxSummaryLen {
		resp = resp[:maxSummaryLen] + "..."
	}
	return resp, nil
}

func (s *SearchAgent) getPreview(text string) string {
	const previewLen = 200
	cleaned := strings.ReplaceAll(text, "\n", " ")
	if len(cleaned) > previewLen {
		return cleaned[:previewLen] + "..."
	}
	return cleaned
}

func (s *SearchAgent) llmCheck(ctx context.Context, query string, facts []string) bool {
	if s.summarizer == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var sb strings.Builder
	for i, f := range facts {
		if i >= 5 {
			sb.WriteString("...")
			break
		}
		sb.WriteString(f)
		sb.WriteString("\n---\n")
	}

	prompt := fmt.Sprintf(`Запрос: "%s"

Факты из базы знаний:
%s
Достаточно ли этих фактов, чтобы полно ответить на запрос?
Ответь только YES или NO.`, query, sb.String())

	resp, err := s.summarizer.CompleteSimple(ctx, s.summarizerModel,
		"Ты — эксперт по оценке полноты информации. Отвечай только YES или NO.",
		prompt, 0.3, 0)
	if err != nil {
		log.Warn("SearchAgent: LLM-проверка кеша ошибка: %v", err)
		return false
	}
	resp = strings.TrimSpace(resp)
	return strings.HasPrefix(resp, "YES")
}

func (s *SearchAgent) factCountGuide() string {
	switch s.verbosity {
	case VerbosityDetailed:
		return "Извлеки 7-15 ключевых фактов или утверждений, которые можно использовать в исследовательском отчёте. Сохрани числовые данные, даты, имена, названия. Если есть прямые цитаты — сохрани их дословно. Не добавляй комментарии от себя. Старайся извлечь максимум полезной информации."
	case VerbosityBrief:
		return "Извлеки 3-5 ключевых фактов или утверждений. Только самое важное. Сохрани числовые данные, даты, имена. Не добавляй комментарии от себя."
	default:
		return "Извлеки 3-7 ключевых фактов или утверждений, которые можно использовать в исследовательском отчёте. Сохрани числовые данные, даты, имена, названия. Если есть прямые цитаты — сохрани их дословно. Не добавляй комментарии от себя."
	}
}

func (s *SearchAgent) maxSummaryLen() int {
	switch s.verbosity {
	case VerbosityDetailed:
		return 6000
	case VerbosityBrief:
		return 1500
	default:
		return 3000
	}
}

func (s *SearchAgent) maxRawLen() int {
	switch s.verbosity {
	case VerbosityDetailed:
		return 20000
	case VerbosityBrief:
		return 5000
	default:
		return 10000
	}
}

func extractLinks(md string) []string {
	var links []string
	for {
		start := strings.Index(md, "](")
		if start == -1 {
			break
		}
		urlStart := start + 2
		end := strings.Index(md[urlStart:], ")")
		if end == -1 {
			break
		}
		url := md[urlStart : urlStart+end]
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			links = append(links, url)
		}
		md = md[urlStart+end+1:]
	}
	return links
}

func hashString(s string) string {
	h := 0
	for _, c := range s {
		h = 31*h + int(c)
	}
	return fmt.Sprintf("%x", h)
}
