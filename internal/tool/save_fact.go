package tool

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/google/uuid"
)

type MemorySaver interface {
    Save(ctx context.Context, key, value string) error
}

type SaveFactTool struct {
    memory MemorySaver
}

func NewSaveFactTool(mem MemorySaver) *SaveFactTool {
    return &SaveFactTool{memory: mem}
}

func (t *SaveFactTool) Name() string { return "save_fact" }

func (t *SaveFactTool) Description() string {
    return `Сохраняет факт в долговременную память. Факт должен быть полезной информацией, которую потом можно найти по смыслу.
Аргументы:
  - fact (строка, обязательный): текст факта. Может содержать ссылки, цитаты.
Пример: {"fact": "Go был создан в Google в 2007 году."}`
}

func (t *SaveFactTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    factRaw, ok := args["fact"]
    if !ok {
        return "", fmt.Errorf("отсутствует обязательный аргумент 'fact'")
    }
    fact, ok := factRaw.(string)
    if !ok {
        return "", fmt.Errorf("аргумент 'fact' должен быть строкой")
    }
    fact = strings.TrimSpace(fact)
    if fact == "" {
        return "", fmt.Errorf("факт не может быть пустым")
    }

    key := fmt.Sprintf("fact_%s_%d", uuid.New().String(), time.Now().UnixNano())
    if err := t.memory.Save(ctx, key, fact); err != nil {
        return "", fmt.Errorf("ошибка сохранения: %w", err)
    }
    return fmt.Sprintf("Факт сохранён (ключ: %s)", key), nil
}
