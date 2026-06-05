package agent

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/icoz/malder/internal/llm"
)

type CritiqueResult struct {
    Score    int    `json:"score"`
    Feedback string `json:"feedback"`
}

type CriticAgent struct {
    llm *llm.Client
}

func NewCriticAgent(llmClient *llm.Client) *CriticAgent {
    return &CriticAgent{llm: llmClient}
}

const criticPromptTemplate = `Ты — строгий, но справедливый критик исследовательских отчётов. 
Оцени представленный ниже отчёт по шкале от 0 до 10, где 0 — полная непригодность, 10 — идеальный отчёт.

Критерии оценки:
- Полнота (охвачены ли все аспекты темы?) – 40%
- Точность (нет ли фактических ошибок, всё ли основано на фактах?) – 30%
- Структура и читаемость (введение, основная часть, заключение, логические переходы) – 20%
- Использование источников (указаны ли ссылки?) – 10%

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

func (c *CriticAgent) Evaluate(ctx context.Context, report string) (int, string, error) {
    log.Println("CriticAgent: оценка отчёта")
    prompt := fmt.Sprintf(criticPromptTemplate, report)
    systemPrompt := "Ты помощник, отвечающий только JSON. Никаких пояснений до или после JSON."
    response, err := c.llm.CompleteSimple(ctx, systemPrompt, prompt)
    if err != nil {
        return 0, "", fmt.Errorf("ошибка LLM: %w", err)
    }
    var result CritiqueResult
    if err := json.Unmarshal([]byte(response), &result); err != nil {
        log.Printf("Невалидный JSON, возвращаем оценку по умолчанию: %s", err)
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
