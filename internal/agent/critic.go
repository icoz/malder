package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/icoz/malder/internal/llm"
	"github.com/icoz/malder/internal/log"
)

type CritiqueResult struct {
	Score    int    `json:"score"`
	Feedback string `json:"feedback"`
}

type CriticAgent struct {
	llm         *llm.Client
	model       string
	temperature float64
	timeout     time.Duration
}

func NewCriticAgent(llmClient *llm.Client, model string, temperature float64, timeout time.Duration) *CriticAgent {
	log.Debug("→ NewCriticAgent(model=%s, temp=%.2f, timeout=%v)", model, temperature, timeout)
	return &CriticAgent{
		llm:         llmClient,
		model:       model,
		temperature: temperature,
		timeout:     timeout,
	}
}

const criticPromptTemplate = `Ты — строгий, но справедливый критик исследовательских отчётов. 
Оцени представленный ниже отчёт по шкале от 0 до 10, где 0 — полная непригодность, 10 — идеальный отчёт.

Критерии оценки:
- Полнота (охвачены ли все аспекты темы?) – 40%%
- Точность (нет ли фактических ошибок, всё ли основано на фактах?) – 30%%
- Структура и читаемость (введение, основная часть, заключение, логические переходы) – 20%%
- Использование источников (указаны ли ссылки?) – 10%%

Твоя задача – выставить итоговую оценку и написать конкретные замечания, указав, каких фактов не хватает, где неточности или плохая структура.

ОТВЕТЬ ТОЛЬКО В ФОРМАТЕ JSON:
{
  "score": число,
  "feedback": "текст замечаний"
}

Не пиши ничего кроме JSON.

--- НАЧАЛО ОТЧЁТА ---
%s
--- КОНЕЦ ОТЧЁТА ---`

func (c *CriticAgent) Evaluate(ctx context.Context, report string) (score int, feedback string, err error) {
	defer func() {
		if err != nil {
			log.Debug("← CriticAgent.Evaluate = (0, \"\", %v)", err)
		} else {
			log.Debug("← CriticAgent.Evaluate = (%d, %q, nil)", score, feedback)
		}
	}()
	log.Debug("→ CriticAgent.Evaluate(report_len=%d)", len(report))
	log.Info("CriticAgent: оценка отчёта (длина=%d)", len(report))
	prompt := fmt.Sprintf(criticPromptTemplate, report)
	systemPrompt := "Ты помощник, отвечающий только JSON. Никаких пояснений до или после JSON."
	llmCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	response, err := c.llm.CompleteSimple(llmCtx, c.model, systemPrompt, prompt, c.temperature)
	if err != nil {
		return 0, "", fmt.Errorf("ошибка LLM: %w", err)
	}
	var result CritiqueResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		log.Warn("Невалидный JSON, возвращаем оценку по умолчанию: %s", err)
		return 5, response, nil
	}
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 10 {
		result.Score = 10
	}
	return result.Score, result.Feedback, nil
}
