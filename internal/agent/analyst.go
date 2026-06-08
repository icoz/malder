package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/tool"
)

type SubReport struct {
	Analysis   string   `json:"analysis"`
	Complete   bool     `json:"complete"`
	GapQueries []string `json:"gap_queries,omitempty"`
	GapReason  string   `json:"gap_reason,omitempty"`
}

type AnalystAgent struct {
	llm          *llm.Client
	memory       *memory.LongTermMemory
	saveFactTool *tool.SaveFactTool
	sourceStore  *memory.SourceStore
	model        string
	temperature  float64
}

func NewAnalystAgent(llmClient *llm.Client, model string, temperature float64, mem *memory.LongTermMemory, saveTool *tool.SaveFactTool, sourceStore *memory.SourceStore) *AnalystAgent {
	return &AnalystAgent{
		llm:          llmClient,
		model:        model,
		temperature:  temperature,
		memory:       mem,
		saveFactTool: saveTool,
		sourceStore:  sourceStore,
	}
}

func (a *AnalystAgent) GenerateSubReport(ctx context.Context, sectionName, subtopicName, topic string) (report *SubReport, err error) {
	defer func() {
		if err != nil {
			log.Debug("← AnalystAgent.GenerateSubReport(%s/%s) = (nil, %v)", sectionName, subtopicName, err)
		} else {
			log.Debug("← AnalystAgent.GenerateSubReport(%s/%s) = (complete=%v, len=%d, nil)", sectionName, subtopicName, report.Complete, len(report.Analysis))
		}
	}()
	log.Debug("→ AnalystAgent.GenerateSubReport(section=%s, subtopic=%s)", sectionName, subtopicName)
	log.Info("AnalystAgent: анализ подтемы %s/%s", sectionName, subtopicName)

	facts, err := a.memory.Recall(ctx, subtopicName)
	if err != nil {
		return nil, fmt.Errorf("ошибка поиска фактов: %w", err)
	}

	var factsText strings.Builder
	for i, f := range facts {
		fact := f
		if len(fact) > 3000 {
			fact = fact[:3000] + "..."
		}
		factsText.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, fact))
	}

	systemPrompt := "Ты — эксперт-аналитик, отвечающий только JSON."
	prompt := fmt.Sprintf(`Исследуемая тема: "%s"
Секция: "%s"
Подтема: "%s"

Факты, собранные из интернета по данной подтеме:
%s

Задание: составь аналитическую заметку по подтеме.
Если фактов достаточно — напиши развёрнутый анализ.
Если фактов явно не хватает (например, их очень мало или они не по теме) —
укажи complete=false и предложи конкретные поисковые запросы для поиска недостающей информации.

Ответь ТОЛЬКО в формате JSON:
{
  "analysis": "текст аналитической заметки на русском языке",
  "complete": true,
  "gap_queries": [],
  "gap_reason": ""
}

Или, если фактов не хватает:
{
  "analysis": "предварительный анализ на основе имеющихся данных",
  "complete": false,
  "gap_queries": ["поисковый запрос 1", "поисковый запрос 2"],
  "gap_reason": "объяснение, каких именно фактов не хватает"
}

Не пиши ничего кроме JSON.`, topic, sectionName, subtopicName, factsText.String())

	resp, err := a.llm.CompleteSimple(ctx, a.model, systemPrompt, prompt, a.temperature)
	if err != nil {
		return nil, fmt.Errorf("ошибка LLM: %w", err)
	}

	var sr SubReport
	if err := json.Unmarshal([]byte(resp), &sr); err != nil {
		return nil, fmt.Errorf("не удалось распарсить ответ аналитика: %s, ошибка: %w", resp, err)
	}

	if sr.Analysis == "" {
		return nil, fmt.Errorf("аналитик вернул пустой анализ")
	}

	if sr.Complete && a.saveFactTool != nil {
		fact := fmt.Sprintf("Источник: аналитический вывод\nСекция: %s\nПодтема: %s\n%s", sectionName, subtopicName, sr.Analysis)
		a.saveFactTool.Execute(ctx, map[string]any{"fact": fact})
	}

	return &sr, nil
}
