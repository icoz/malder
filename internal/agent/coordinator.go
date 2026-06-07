package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
)

type ProgressReporter func(event string, data map[string]any)

type ResearchResult struct {
	Report     string
	SourceURLs []string
}

type CoordinatorAgent struct {
	llm                    *llm.Client
	model                  string
	temperature            float64
	memory                 *memory.LongTermMemory
	sourceStore            *memory.SourceStore
	searchAgent            *SearchAgent
	analystAgent           *AnalystAgent
	criticAgent            *CriticAgent
	maxIterations          int
	maxConcurrentSubtopics int
	maxSubtopicRetries     int
	reporter               ProgressReporter
}

type CoordinatorConfig struct {
	LLM                    *llm.Client
	Model                  string
	Temperature            float64
	Memory                 *memory.LongTermMemory
	SourceStore            *memory.SourceStore
	SearchAgent            *SearchAgent
	AnalystAgent           *AnalystAgent
	CriticAgent            *CriticAgent
	MaxIterations          int
	MaxConcurrentSubtopics int
	MaxSubtopicRetries     int
}

func NewCoordinator(cfg CoordinatorConfig) *CoordinatorAgent {
	log.Debug("→ NewCoordinator(maxIter=%d, maxConcurrent=%d, maxRetries=%d)", cfg.MaxIterations, cfg.MaxConcurrentSubtopics, cfg.MaxSubtopicRetries)
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 3
	}
	if cfg.MaxConcurrentSubtopics == 0 {
		cfg.MaxConcurrentSubtopics = 3
	}
	if cfg.MaxSubtopicRetries == 0 {
		cfg.MaxSubtopicRetries = 2
	}
	return &CoordinatorAgent{
		llm:                    cfg.LLM,
		model:                  cfg.Model,
		temperature:            cfg.Temperature,
		memory:                 cfg.Memory,
		sourceStore:            cfg.SourceStore,
		searchAgent:            cfg.SearchAgent,
		analystAgent:           cfg.AnalystAgent,
		criticAgent:            cfg.CriticAgent,
		maxIterations:          cfg.MaxIterations,
		maxConcurrentSubtopics: cfg.MaxConcurrentSubtopics,
		maxSubtopicRetries:     cfg.MaxSubtopicRetries,
	}
}

func (c *CoordinatorAgent) SetProgressReporter(reporter ProgressReporter) {
	c.reporter = reporter
}

func (c *CoordinatorAgent) LLM() *llm.Client              { return c.llm }
func (c *CoordinatorAgent) Memory() *memory.LongTermMemory { return c.memory }
func (c *CoordinatorAgent) SearchAgent() *SearchAgent      { return c.searchAgent }
func (c *CoordinatorAgent) AnalystAgent() *AnalystAgent    { return c.analystAgent }
func (c *CoordinatorAgent) CriticAgent() *CriticAgent      { return c.criticAgent }
func (c *CoordinatorAgent) MaxIterations() int             { return c.maxIterations }
func (c *CoordinatorAgent) Model() string                  { return c.model }
func (c *CoordinatorAgent) Temperature() float64           { return c.temperature }
func (c *CoordinatorAgent) MaxConcurrentSubtopics() int    { return c.maxConcurrentSubtopics }
func (c *CoordinatorAgent) MaxSubtopicRetries() int        { return c.maxSubtopicRetries }
func (c *CoordinatorAgent) SourceStore() *memory.SourceStore { return c.sourceStore }

func (c *CoordinatorAgent) Run(ctx context.Context, userQuery string) (result *ResearchResult, err error) {
	defer func() {
		if err != nil {
			log.Debug("← CoordinatorAgent.Run(%s) = (nil, %v)", userQuery, err)
		} else {
			log.Debug("← CoordinatorAgent.Run(%s) = (len=%d, nil)", userQuery, len(result.Report))
		}
	}()
	log.Debug("→ CoordinatorAgent.Run(query=%s, maxIter=%d)", userQuery, c.maxIterations)

	c.report("start", map[string]any{"query": userQuery})

	c.report("planning", nil)
	plan, err := c.createPlan(ctx, userQuery)
	if err != nil {
		return nil, fmt.Errorf("планирование не удалось: %w", err)
	}
	log.Info("Coordinator: план исследования — %d секций", len(plan.Sections))
	for i, s := range plan.Sections {
		log.Info("Coordinator: секция %d: %s (%d подтем)", i+1, s.Name, len(s.Subtopics))
	}
	c.report("plan_complete", map[string]any{
		"title":    plan.Title,
		"sections": len(plan.Sections),
	})

	allQueries := flattenQueries(plan)
	log.Info("Coordinator: всего поисковых запросов: %d", len(allQueries))
	c.report("search_start", nil)
	if err := c.searchAgent.Run(ctx, allQueries); err != nil {
		log.Warn("Ошибки при поиске: %v", err)
	}
	c.report("search_complete", nil)

	c.report("subtopic_analysis_start", nil)
	subResults := c.researchSubtopics(ctx, plan)
	c.report("subtopic_analysis_complete", map[string]any{"completed": len(subResults)})

	c.report("section_synthesis_start", nil)
	sectionReports := c.synthesizeSections(ctx, plan, subResults)
	c.report("section_synthesis_complete", map[string]any{"sections": len(sectionReports)})

	c.report("critic_loop_start", nil)
	result, err = c.criticLoop(ctx, plan.Title, sectionReports)
	if err != nil {
		return nil, fmt.Errorf("критический цикл не удался: %w", err)
	}

	c.report("finish", map[string]any{"result": result.Report})
	return result, nil
}

func (c *CoordinatorAgent) researchSubtopics(ctx context.Context, plan *ResearchPlan) map[string]*SubReport {
	type job struct {
		section  Section
		subtopic Subtopic
	}

	var jobs []job
	for _, section := range plan.Sections {
		for _, subtopic := range section.Subtopics {
			jobs = append(jobs, job{section, subtopic})
		}
	}

	type result struct {
		key string
		rep *SubReport
		err error
	}

	results := make(chan result, len(jobs))
	sem := make(chan struct{}, c.maxConcurrentSubtopics)

	for _, j := range jobs {
		go func(section Section, subtopic Subtopic) {
			sem <- struct{}{}
			defer func() { <-sem }()

			key := section.Name + "|" + subtopic.Name

			for attempt := 0; attempt < c.maxSubtopicRetries; attempt++ {
				rep, err := c.analystAgent.GenerateSubReport(ctx, section.Name, subtopic.Name, plan.Title)
				if err != nil {
					results <- result{key, nil, err}
					return
				}

				if rep.Complete {
					results <- result{key, rep, nil}
					return
				}

				log.Info("Coordinator: подтема '%s' — не хватает фактов, ищем (попытка %d/%d)", key, attempt+1, c.maxSubtopicRetries)
				if len(rep.GapQueries) > 0 {
					if err := c.searchAgent.Run(ctx, rep.GapQueries); err != nil {
						log.Warn("Ошибка при поиске gap-запросов для '%s': %v", key, err)
					}
				}
			}

			rep, err := c.analystAgent.GenerateSubReport(ctx, section.Name, subtopic.Name, plan.Title)
			if err != nil {
				results <- result{key, nil, err}
				return
			}
			results <- result{key, rep, nil}
		}(j.section, j.subtopic)
	}

	subReports := make(map[string]*SubReport)
	for i := 0; i < len(jobs); i++ {
		r := <-results
		if r.err != nil {
			log.Warn("Coordinator: ошибка подтемы '%s': %v", r.key, r.err)
			continue
		}
		subReports[r.key] = r.rep
	}

	return subReports
}

func (c *CoordinatorAgent) synthesizeSections(ctx context.Context, plan *ResearchPlan, subReports map[string]*SubReport) []string {
	type sectionResult struct {
		index  int
		report string
		err    error
	}

	results := make(chan sectionResult, len(plan.Sections))
	sem := make(chan struct{}, c.maxConcurrentSubtopics)

	for i, section := range plan.Sections {
		go func(idx int, sec Section) {
			sem <- struct{}{}
			defer func() { <-sem }()

			var analyses []string
			for _, sub := range sec.Subtopics {
				key := sec.Name + "|" + sub.Name
				if rep, ok := subReports[key]; ok {
					analyses = append(analyses, rep.Analysis)
				}
			}

			if len(analyses) == 0 {
				results <- sectionResult{idx, "", fmt.Errorf("нет данных для секции '%s'", sec.Name)}
				return
			}

			report, err := c.synthesizeSection(ctx, sec.Name, analyses)
			if err != nil {
				results <- sectionResult{idx, "", err}
				return
			}

			c.saveToMemory(ctx, "section_"+sec.Name, fmt.Sprintf("Источник: аналитический синтез\nРаздел: %s\n\n%s", sec.Name, report))
			if c.sourceStore != nil {
				c.sourceStore.Put(memory.Provenance{
					Key:     "section_" + sec.Name,
					Kind:    "section",
					Preview: getPreview(report),
					IsRaw:   true,
				})
			}
			results <- sectionResult{idx, report, nil}
		}(i, section)
	}

	sectionReports := make([]string, len(plan.Sections))
	for i := 0; i < len(plan.Sections); i++ {
		r := <-results
		if r.err != nil {
			log.Warn("Coordinator: ошибка синтеза секции #%d: %v", r.index, r.err)
			continue
		}
		sectionReports[r.index] = r.report
	}

	return sectionReports
}

func (c *CoordinatorAgent) criticLoop(ctx context.Context, title string, sectionReports []string) (*ResearchResult, error) {
	var finalReport string
	var sourceURLs []string
	for iteration := 1; iteration <= c.maxIterations; iteration++ {
		c.report("synthesis_start", map[string]any{"iteration": iteration})

		report, urls, err := c.synthesizeFinal(ctx, title, sectionReports)
		if err != nil {
			return nil, fmt.Errorf("ошибка синтеза отчёта: %w", err)
		}
		finalReport = report
		sourceURLs = urls
		c.report("synthesis_complete", map[string]any{"iteration": iteration, "report_length": len(report)})

		key := fmt.Sprintf("final_%d", time.Now().UnixNano())
		c.saveToMemory(ctx, key, fmt.Sprintf("Источник: итоговый синтез\nИтерация: %d\n%s", iteration, report))
		if c.sourceStore != nil {
			c.sourceStore.Put(memory.Provenance{
				Key: key, Kind: "final",
				Preview: getPreview(report), IsRaw: true,
			})
		}

		if iteration == c.maxIterations {
			break
		}

		c.report("critic_start", map[string]any{"iteration": iteration})
		score, feedback, err := c.criticAgent.Evaluate(ctx, finalReport)
		if err != nil {
			log.Warn("Критик вернул ошибку: %v", err)
			break
		}
		c.report("critic_complete", map[string]any{"score": score, "feedback": feedback})

		if score >= 7 {
			log.Info("Оценка %d >= 7, качество достаточное", score)
			break
		}

		c.report("additional_search_start", map[string]any{"feedback": feedback})
		additionalQueries := c.extractQueriesFromFeedback(ctx, feedback)
		if len(additionalQueries) > 0 {
			if err := c.searchAgent.Run(ctx, additionalQueries); err != nil {
				log.Warn("Ошибки при дополнительном поиске: %v", err)
			}
		}
		c.report("additional_search_complete", nil)
	}

	return &ResearchResult{Report: finalReport, SourceURLs: sourceURLs}, nil
}

func (c *CoordinatorAgent) createPlan(ctx context.Context, userQuery string) (plan *ResearchPlan, err error) {
	defer func() {
		log.Debug("← CoordinatorAgent.createPlan = (%v, %v)", plan, err)
	}()
	log.Debug("→ CoordinatorAgent.createPlan(query=%s)", userQuery)
	systemPrompt := "Ты — планировщик исследований, отвечающий только JSON."
	prompt := fmt.Sprintf(`Пользователь задал тему исследования: "%s"

Составь структурированный план исследования в формате JSON.
План должен содержать:
- title: название исследования
- sections: массив секций (разделов) исследования

Каждая секция содержит:
- name: название секции
- subtopics: массив подтем

Каждая подтема содержит:
- name: название подтемы
- queries: массив из 2-3 конкретных поисковых запросов для сбора информации по этой подтеме

Требования:
- 2-4 секции
- 2-3 подтемы в каждой секции
- Запросы должны быть на русском языке (для поиска в Яндексе/Google)
- Запросы должны быть конкретными и релевантными

Пример формата:
{
  "title": "Название исследования",
  "sections": [
    {
      "name": "Название секции",
      "subtopics": [
        {
          "name": "Название подтемы",
          "queries": ["поисковый запрос 1", "поисковый запрос 2"]
        }
      ]
    }
  ]
}

Не пиши ничего кроме JSON.`, userQuery)

	response, err := c.llm.CompleteSimple(ctx, c.model, systemPrompt, prompt, c.temperature)
	if err != nil {
		return nil, err
	}

	var p ResearchPlan
	if err := json.Unmarshal([]byte(response), &p); err != nil {
		return nil, fmt.Errorf("не удалось распарсить план: %s, ошибка: %w", response, err)
	}
	if p.Title == "" || len(p.Sections) == 0 {
		return nil, fmt.Errorf("планировщик вернул некорректный план")
	}
	return &p, nil
}

func (c *CoordinatorAgent) synthesizeSection(ctx context.Context, sectionName string, analyses []string) (string, error) {
	log.Debug("→ CoordinatorAgent.synthesizeSection(name=%s, analyses=%d)", sectionName, len(analyses))

	var sb string
	for i, a := range analyses {
		sb += fmt.Sprintf("Подтема %d:\n%s\n\n", i+1, a)
	}

	systemPrompt := "Ты — составитель разделов исследовательских отчётов."
	prompt := fmt.Sprintf(`Название секции: "%s"

Ниже приведены аналитические заметки по подтемам этой секции.
Объедини их в связный, хорошо структурированный раздел отчёта.
Убери дублирующуюся информацию, добавь логические переходы между подтемами.
Раздел должен читаться как единое целое.

Заметки по подтемам:
%s

Напиши раздел отчёта на русском языке.`, sectionName, sb)

	report, err := c.llm.CompleteSimple(ctx, c.model, systemPrompt, prompt, c.temperature)
	if err != nil {
		return "", fmt.Errorf("ошибка синтеза секции: %w", err)
	}
	return report, nil
}

func (c *CoordinatorAgent) synthesizeFinal(ctx context.Context, title string, sectionReports []string) (string, []string, error) {
	log.Debug("→ CoordinatorAgent.synthesizeFinal(title=%s, sections=%d)", title, len(sectionReports))

	var sb string
	for i, sr := range sectionReports {
		sb += fmt.Sprintf("=== Раздел %d ===\n%s\n\n", i+1, sr)
	}

	// Collect source URLs from memory
	var sourcesText string
	var sourceURLs []string
	if c.memory != nil {
		facts, err := c.memory.Recall(ctx, title)
		if err == nil {
			seen := make(map[string]bool)
			for _, f := range facts {
				for _, line := range strings.Split(f, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "Источник:") {
						url := strings.TrimSpace(strings.TrimPrefix(line, "Источник:"))
						if strings.HasPrefix(url, "http") && !seen[url] {
							sourceURLs = append(sourceURLs, url)
							seen[url] = true
						}
					}
				}
			}
			if len(sourceURLs) > 0 {
				sourcesText = "\nИсточники для цитирования:\n" + strings.Join(sourceURLs, "\n")
			}
		}
	}

	systemPrompt := "Ты — автор исследовательских отчётов."
	prompt := fmt.Sprintf(`Тема исследования: "%s"

Ниже приведены разделы отчёта, подготовленные отдельными аналитиками.
Объедини их в итоговый, хорошо структурированный отчёт.

Разделы:
%s

Требования:
- Напиши введение: актуальность темы, цель исследования
- Объедини разделы логическими переходами
- Напиши заключение: выводы, рекомендации
- Если в разделах есть ссылки на источники, собери их в итоговый список в конце
- При цитировании конкретных данных указывай URL из списка источников%s

Отчёт пиши на русском языке.`, title, sb, sourcesText)

	report, err := c.llm.CompleteSimple(ctx, c.model, systemPrompt, prompt, c.temperature)
	if err != nil {
		return "", nil, fmt.Errorf("ошибка синтеза финального отчёта: %w", err)
	}
	return report, sourceURLs, nil
}

func (c *CoordinatorAgent) extractQueriesFromFeedback(ctx context.Context, feedback string) (queries []string) {
	defer func() {
		log.Debug("← CoordinatorAgent.extractQueriesFromFeedback = %v", queries)
	}()
	log.Debug("→ CoordinatorAgent.extractQueriesFromFeedback(feedback_len=%d)", len(feedback))
	prompt := fmt.Sprintf(`Ты – помощник, который превращает замечания критика в конкретные поисковые запросы для интернета. 
Замечания критика: %s
Сформулируй 2-3 поисковых запроса, которые помогут найти недостающую информацию.
Ответь ТОЛЬКО в формате JSON-массива строк, например: ["запрос 1", "запрос 2"]
Не пиши ничего кроме JSON.`, feedback)

	response, err := c.llm.CompleteSimple(ctx, c.model, "Ты полезный помощник.", prompt, c.temperature)
	if err != nil {
		log.Warn("Ошибка при генерации доп. запросов: %v", err)
		return nil
	}
	if err := json.Unmarshal([]byte(response), &queries); err != nil {
		log.Warn("Не удалось распарсить JSON: %s", response)
		return nil
	}
	return
}

func (c *CoordinatorAgent) saveToMemory(ctx context.Context, key, value string) {
	if err := c.memory.Save(ctx, key, value); err != nil {
		log.Warn("Coordinator: ошибка сохранения '%s' в память: %v", key, err)
	} else {
		log.Info("Coordinator: сохранено в память: %s", key)
	}
}

func (c *CoordinatorAgent) report(event string, data map[string]any) {
	if c.reporter != nil {
		c.reporter(event, data)
	}
}

func flattenQueries(plan *ResearchPlan) []string {
	var queries []string
	seen := make(map[string]bool)
	for _, section := range plan.Sections {
		for _, subtopic := range section.Subtopics {
			for _, q := range subtopic.Queries {
				if !seen[q] {
					queries = append(queries, q)
					seen[q] = true
				}
			}
		}
	}
	return queries
}

func getPreview(text string) string {
	const n = 200
	cleaned := strings.ReplaceAll(text, "\n", " ")
	if len(cleaned) > n {
		return cleaned[:n] + "..."
	}
	return cleaned
}
