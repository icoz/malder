package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
)

type RecallFactsTool struct {
	memory *memory.LongTermMemory
}

func NewRecallFactsTool(mem *memory.LongTermMemory) *RecallFactsTool {
	return &RecallFactsTool{memory: mem}
}

func (t *RecallFactsTool) Name() string { return "recall_facts" }

func (t *RecallFactsTool) Description() string {
	return `Ищет факты в долговременной памяти по смыслу.
Аргументы:
  - query (строка, обязательный): запрос на поиск.
  - topK (целое, необязательный): количество результатов (по умолчанию 5)
Возвращает список найденных фактов.`
}

func (t *RecallFactsTool) Execute(ctx context.Context, args map[string]any) (result string, err error) {
	defer func() {
		log.Debug("← RecallFactsTool.Execute = (len=%d, %v)", len(result), err)
	}()
	log.Debug("→ RecallFactsTool.Execute(args=%v)", args)
	queryRaw, ok := args["query"]
	if !ok {
		return "", fmt.Errorf("нет query")
	}
	query, ok := queryRaw.(string)
	if !ok {
		return "", fmt.Errorf("query не строка")
	}
	topK := 5
	if topKRaw, ok := args["topK"]; ok {
		if v, ok := topKRaw.(float64); ok {
			topK = int(v)
		}
	}
	facts, err := t.memory.RecallWithTopK(ctx, query, topK)
	if err != nil {
		return "", err
	}
	if len(facts) == 0 {
		return "Фактов по запросу не найдено.", nil
	}
	return strings.Join(facts, "\n---\n"), nil
}
