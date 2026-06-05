package memory

import (
    "context"
    "fmt"
    "sync"

    "github.com/philippgille/chromem-go"
)

type LongTermMemory struct {
    db   *chromem.DB
    mu   sync.RWMutex
    kv   map[string]string
    path string
}

func NewLongTermMemory(persistPath string) (*LongTermMemory, error) {
    var db *chromem.DB
    var err error
    if persistPath != "" {
        db, err = chromem.NewPersistentDB(persistPath, false)
        if err != nil {
            return nil, fmt.Errorf("не удалось открыть persistent DB: %w", err)
        }
    } else {
        db = chromem.NewDB()
    }
    return &LongTermMemory{
        db:   db,
        kv:   make(map[string]string),
        path: persistPath,
    }, nil
}

func (m *LongTermMemory) ensureCollection(ctx context.Context) (*chromem.Collection, error) {
    return m.db.GetOrCreateCollection("facts", nil, nil)
}

func (m *LongTermMemory) Save(ctx context.Context, key, value string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.kv[key] = value
    coll, err := m.ensureCollection(ctx)
    if err != nil {
        return fmt.Errorf("chromem get collection: %w", err)
    }
    if err := coll.AddDocument(ctx, chromem.Document{
        ID:      key,
        Content: value,
    }); err != nil {
        return fmt.Errorf("chromem add: %w", err)
    }
    return nil
}

func (m *LongTermMemory) Load(key string) (string, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    val, ok := m.kv[key]
    return val, ok
}

func (m *LongTermMemory) Recall(ctx context.Context, query string) ([]string, error) {
    return m.RecallWithTopK(ctx, query, 5)
}

func (m *LongTermMemory) RecallWithTopK(ctx context.Context, query string, topK int) ([]string, error) {
    coll := m.db.GetCollection("facts", nil)
    if coll == nil {
        return []string{}, nil
    }
    results, err := coll.Query(ctx, query, topK, nil, nil)
    if err != nil {
        return nil, fmt.Errorf("query chromem: %w", err)
    }
    facts := make([]string, len(results))
    for i, res := range results {
        facts[i] = res.Content
    }
    return facts, nil
}

func (m *LongTermMemory) Close() error {
    return nil
}
