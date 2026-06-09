package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
)

type KnowledgeSearchTool struct {
	memory *memory.LongTermMemory
}

func NewKnowledgeSearchTool(mem *memory.LongTermMemory) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{memory: mem}
}

func (t *KnowledgeSearchTool) Name() string { return "knowledge_search" }

func (t *KnowledgeSearchTool) Description() string {
	return `Ищет информацию в базе знаний (загруженные документы) по смыслу.
Аргументы:
  - query (строка, обязательный): запрос на поиск.
  - topK (целое, необязательный): количество результатов (по умолчанию 5)
Возвращает фрагменты документов, релевантные запросу.`
}

func (t *KnowledgeSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	log.Debug("→ KnowledgeSearchTool.Execute(args=%v)", args)
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
	chunks, err := t.memory.RecallKnowledge(ctx, query, topK)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "В базе знаний ничего не найдено.", nil
	}
	return strings.Join(chunks, "\n---\n"), nil
}
