package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
)

type ProgressReporter func(event string, data map[string]any)

type CoordinatorAgent struct {
	llm           *llm.Client
	model         string
	temperature   float64
	timeout       time.Duration
	memory        *memory.LongTermMemory
	searchAgent   *SearchAgent
	analystAgent  *AnalystAgent
	criticAgent   *CriticAgent
	maxIterations int
	reporter      ProgressReporter
}

type CoordinatorConfig struct {
	LLM           *llm.Client
	Model         string
	Temperature   float64
	Timeout       time.Duration
	Memory        *memory.LongTermMemory
	SearchAgent   *SearchAgent
	AnalystAgent  *AnalystAgent
	CriticAgent   *CriticAgent
	MaxIterations int
}

func NewCoordinator(cfg CoordinatorConfig) *CoordinatorAgent {
	log.Debug("→ NewCoordinator(maxIter=%d, timeout=%v)", cfg.MaxIterations, cfg.Timeout)
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 3
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &CoordinatorAgent{
		llm:           cfg.LLM,
		model:         cfg.Model,
		temperature:   cfg.Temperature,
		timeout:       cfg.Timeout,
		memory:        cfg.Memory,
		searchAgent:   cfg.SearchAgent,
		analystAgent:  cfg.AnalystAgent,
		criticAgent:   cfg.CriticAgent,
		maxIterations: cfg.MaxIterations,
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
func (c *CoordinatorAgent) Timeout() time.Duration         { return c.timeout }

func (c *CoordinatorAgent) Run(ctx context.Context, userQuery string) (result string, err error) {
	defer func() {
		if err != nil {
			log.Debug("← CoordinatorAgent.Run(%s) = (\"\", %v)", userQuery, err)
		} else {
			log.Debug("← CoordinatorAgent.Run(%s) = (len=%d, nil)", userQuery, len(result))
		}
	}()
	log.Debug("→ CoordinatorAgent.Run(query=%s, maxIter=%d)", userQuery, c.maxIterations)

	c.report("start", map[string]any{"query": userQuery})

	c.report("planning", nil)
	searchQueries, err := c.planQueries(ctx, userQuery)
	if err != nil {
		return "", fmt.Errorf("планирование не удалось: %w", err)
	}
	c.report("plan_complete", map[string]any{"queries": searchQueries})

	c.report("search_start", nil)
	if err := c.searchAgent.Run(ctx, searchQueries); err != nil {
		log.Warn("Ошибки при поиске: %v", err)
	}
	c.report("search_complete", nil)

	var currentReport string
	for iteration := 1; iteration <= c.maxIterations; iteration++ {
		c.report("analysis_start", map[string]any{"iteration": iteration})
		report, err := c.analystAgent.GenerateReport(ctx, userQuery)
		if err != nil {
			return "", fmt.Errorf("ошибка аналитика на итерации %d: %w", iteration, err)
		}
		currentReport = report
		c.report("analysis_complete", map[string]any{"iteration": iteration, "report_length": len(report)})

		if iteration == c.maxIterations {
			break
		}

		c.report("critic_start", map[string]any{"iteration": iteration})
		score, feedback, err := c.criticAgent.Evaluate(ctx, currentReport)
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

	c.report("finish", map[string]any{"result": currentReport})
	return currentReport, nil
}

func (c *CoordinatorAgent) planQueries(ctx context.Context, userQuery string) (queries []string, err error) {
	defer func() {
		log.Debug("← CoordinatorAgent.planQueries = (%v, %v)", queries, err)
	}()
	log.Debug("→ CoordinatorAgent.planQueries(query=%s)", userQuery)
	prompt := fmt.Sprintf(`Ты — планировщик исследовательской системы. 
Пользователь задал тему: "%s"
Разбей эту тему на 3–5 конкретных поисковых запросов, которые помогут собрать информацию.
Ответь ТОЛЬКО в формате JSON-массива строк, например: ["запрос 1", "запрос 2", "запрос 3"]
Не пиши ничего кроме JSON.`, userQuery)

	response, err := c.llm.CompleteSimple(ctx, c.model, "Ты помощник, отвечающий только JSON.", prompt, c.temperature)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(response), &queries); err != nil {
		return nil, fmt.Errorf("не удалось распарсить JSON: %s, ошибка: %w", response, err)
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("планировщик не сгенерировал запросы")
	}
	return queries, nil
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

func (c *CoordinatorAgent) report(event string, data map[string]any) {
	if c.reporter != nil {
		c.reporter(event, data)
	}
}
