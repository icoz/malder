package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/tool"
)

type AnalystAgent struct {
	llm          *llm.Client
	memory       *memory.LongTermMemory
	saveFactTool *tool.SaveFactTool
	model        string
	temperature  float64
}

func NewAnalystAgent(llmClient *llm.Client, model string, temperature float64, mem *memory.LongTermMemory, saveTool *tool.SaveFactTool) *AnalystAgent {
	return &AnalystAgent{
		llm:          llmClient,
		model:        model,
		temperature:  temperature,
		memory:       mem,
		saveFactTool: saveTool,
	}
}

func (a *AnalystAgent) GenerateReport(ctx context.Context, topic string) (string, error) {
	log.Printf("AnalystAgent: анализ темы: %s", topic)

	facts, err := a.memory.Recall(ctx, topic)
	if err != nil {
		return "", fmt.Errorf("ошибка поиска фактов: %w", err)
	}
	if len(facts) == 0 {
		return "Недостаточно фактов для анализа по теме \"" + topic + "\". Попробуйте уточнить запрос.", nil
	}

	prompt := a.buildPrompt(topic, facts)
	systemPrompt := "Ты — эксперт-аналитик. Составляй чёткие, структурированные отчёты на русском языке. Используй факты из предоставленного контекста. Если факты противоречивы, укажи это."
	report, err := a.llm.CompleteSimple(ctx, a.model, systemPrompt, prompt, a.temperature)
	if err != nil {
		return "", fmt.Errorf("ошибка LLM: %w", err)
	}

	if a.saveFactTool != nil {
		a.saveFactTool.Execute(ctx, map[string]any{
			"fact": fmt.Sprintf("Отчёт по теме '%s':\n%s", topic, report),
		})
	}
	return report, nil
}

func (a *AnalystAgent) buildPrompt(topic string, facts []string) string {
	var factsText strings.Builder
	for i, f := range facts {
		fact := f
		if len(fact) > 2000 {
			fact = fact[:2000] + "..."
		}
		factsText.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, fact))
	}
	return fmt.Sprintf(`Тема исследования: %s

Найденные факты (из интернета и ранее сохранённые аналитические выводы):
%s

Задание: составь аналитический отчёт по теме. Отчёт должен включать:
- Краткое введение (актуальность темы)
- Основную часть (ключевые факты, тренды, аргументы)
- Заключение (выводы, рекомендации, если применимо)
- Список источников (укажи ссылки, если они есть в фактах)

Если фактов недостаточно, честно укажи это в отчёте. Не выдумывай информацию.
Отчёт пиши на русском языке.`, topic, factsText.String())
}
