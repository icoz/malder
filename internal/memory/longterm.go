package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/icoz/malder/internal/log"
	"github.com/philippgille/chromem-go"
)

type LongTermMemory struct {
	db        *chromem.DB
	mu        sync.RWMutex
	kv        map[string]string
	path      string
	count     int
	embedFunc chromem.EmbeddingFunc
	topK      int
}

func NewLongTermMemory(persistPath, embedEndpoint, embedAPIKey, embedModel string, topK int) (*LongTermMemory, error) {
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
	if topK <= 0 {
		topK = 15
	}
	return &LongTermMemory{
		db:        db,
		kv:        make(map[string]string),
		path:      persistPath,
		embedFunc: chromem.NewEmbeddingFuncOpenAICompat(embedEndpoint, embedAPIKey, embedModel, nil),
		topK:      topK,
	}, nil
}

func (m *LongTermMemory) ensureCollection(ctx context.Context) (*chromem.Collection, error) {
	return m.db.GetOrCreateCollection("facts", nil, m.embedFunc)
}

func (m *LongTermMemory) Save(ctx context.Context, key, value string) (err error) {
	defer func() {
		log.Debug("← LongTermMemory.Save(%s, len=%d) = %v", key, len(value), err)
	}()
	log.Debug("→ LongTermMemory.Save(key=%s, value_len=%d)", key, len(value))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[key] = value
	m.count = len(m.kv)
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
	log.Info("Память: сохранён факт, всего фактов: %d", m.count)
	return nil
}

func (m *LongTermMemory) Load(key string) (val string, ok bool) {
	defer func() {
		log.Debug("← LongTermMemory.Load(%s) = (%q, %v)", key, val, ok)
	}()
	log.Debug("→ LongTermMemory.Load(key=%s)", key)
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok = m.kv[key]
	return
}

func (m *LongTermMemory) TopK() int { return m.topK }

func (m *LongTermMemory) Recall(ctx context.Context, query string) (facts []string, err error) {
	facts, _, err = m.RecallWithTopK(ctx, query, m.topK)
	log.Debug("← LongTermMemory.Recall(%s) = (len=%d, %v)", query, len(facts), err)
	return
}

func (m *LongTermMemory) RecallWithTopK(ctx context.Context, query string, topK int) (facts []string, avgDistance float64, err error) {
	defer func() {
		log.Debug("← LongTermMemory.RecallWithTopK(%s, %d) = (len=%d, avgDist=%.3f, %v)", query, topK, len(facts), avgDistance, err)
	}()
	log.Debug("→ LongTermMemory.RecallWithTopK(query=%s, topK=%d)", query, topK)
	coll := m.db.GetCollection("facts", m.embedFunc)
	if coll == nil {
		return []string{}, 0, nil
	}
	results, err := coll.Query(ctx, query, topK, nil, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("query chromem: %w", err)
	}
	facts = make([]string, len(results))
	var totalSim float64
	for i, res := range results {
		facts[i] = res.Content
		totalSim += float64(res.Similarity)
	}
	if len(results) > 0 {
		avgDistance = 1.0 - totalSim/float64(len(results))
	}
	return facts, avgDistance, nil
}

func (m *LongTermMemory) Close() error {
	return nil
}
