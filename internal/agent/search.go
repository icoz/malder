package agent

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/scheduler"
	"github.com/icoz/malder/internal/tool"
)

var linkRegex = regexp.MustCompile(`Ссылка:\s*(https?://[^\s\n]+)`)

type SearchAgent struct {
	searchTool       *tool.SearchTool
	fetchTool        *tool.FetchPageTool
	memory           *memory.LongTermMemory
	scheduler        *scheduler.AdaptiveScheduler
	maxPagesPerQuery int
}

func NewSearchAgent(
	searchTool *tool.SearchTool,
	fetchTool *tool.FetchPageTool,
	mem *memory.LongTermMemory,
	sched *scheduler.AdaptiveScheduler,
	maxPagesPerQuery int,
) *SearchAgent {
	log.Debug("→ NewSearchAgent(maxPages=%d)", maxPagesPerQuery)
	if maxPagesPerQuery <= 0 {
		maxPagesPerQuery = 3
	}
	return &SearchAgent{
		searchTool:       searchTool,
		fetchTool:        fetchTool,
		memory:           mem,
		scheduler:        sched,
		maxPagesPerQuery: maxPagesPerQuery,
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
	searchResult, err := s.searchTool.Execute(ctx, map[string]any{"query": query, "num": s.maxPagesPerQuery + 2})
	if err != nil {
		return fmt.Errorf("поиск не удался: %w", err)
	}

	links := extractLinks(searchResult)
	if len(links) == 0 {
		log.Info("SearchAgent: для запроса '%s' нет ссылок", query)
		return nil
	}
	if len(links) > s.maxPagesPerQuery {
		links = links[:s.maxPagesPerQuery]
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	for _, link := range links {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := s.processPage(ctx, url, query); err != nil {
				log.Warn("Ошибка загрузки %s: %v", url, err)
			}
		}(link)
	}
	wg.Wait()
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
	const maxFactLen = 5000
	if len(content) > maxFactLen {
		content = content[:maxFactLen] + "... (обрезано)"
	}
	fact := fmt.Sprintf("Источник: %s\nПо запросу: %s\nСодержимое:\n%s", pageURL, query, content)
	key := fmt.Sprintf("page_%d_%s", time.Now().UnixNano(), hashString(pageURL))
	if err := s.memory.Save(ctx, key, fact); err != nil {
		return fmt.Errorf("сохранение факта: %w", err)
	}
	log.Info("SearchAgent: сохранена страница %s (%d символов)", pageURL, len(content))
	return nil
}

func extractLinks(result string) []string {
	matches := linkRegex.FindAllStringSubmatch(result, -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 2 {
			links = append(links, m[1])
		}
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
