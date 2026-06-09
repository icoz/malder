package memory

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/icoz/malder/internal/log"
	"github.com/philippgille/chromem-go"
)

// RetryConfig настраивает ретраи для операций с embedding API.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

type LongTermMemory struct {
	db        *chromem.DB
	mu        sync.RWMutex
	kv        map[string]string
	path      string
	count     int
	embedFunc chromem.EmbeddingFunc
	topK      int

	retryMaxAttempts int
	retryBaseDelay   time.Duration
}

func NewLongTermMemory(persistPath, embedEndpoint, embedAPIKey, embedModel string, topK int, retryCfg *RetryConfig) (*LongTermMemory, error) {
	retryAttempts := 3
	retryDelay := 1 * time.Second
	if retryCfg != nil {
		if retryCfg.MaxAttempts > 0 {
			retryAttempts = retryCfg.MaxAttempts
		}
		if retryCfg.BaseDelay > 0 {
			retryDelay = retryCfg.BaseDelay
		}
	}
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

		retryMaxAttempts: retryAttempts,
		retryBaseDelay:   retryDelay,
	}, nil
}

func (m *LongTermMemory) ensureCollection(ctx context.Context) (*chromem.Collection, error) {
	return m.db.GetOrCreateCollection("facts", nil, m.embedFunc)
}

func (m *LongTermMemory) ensureKnowledgeCollection(ctx context.Context) (*chromem.Collection, error) {
	return m.db.GetOrCreateCollection("knowledge", nil, m.embedFunc)
}

// jitter returns a duration in [d*0.5, d*1.5).
func jitter(d time.Duration) time.Duration {
	half := int64(d) / 2
	n, err := rand.Int(rand.Reader, big.NewInt(half))
	if err != nil {
		return d
	}
	return time.Duration(int64(d) - half + n.Int64())
}

func (m *LongTermMemory) retryWithBackoff(ctx context.Context, op string, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < m.retryMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := jitter(m.retryBaseDelay * time.Duration(1<<(attempt-1)))
			log.Warn("Embedding %s failed (attempt %d/%d): %v, retrying in %v", op, attempt+1, m.retryMaxAttempts, lastErr, delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("embedding %s failed after %d attempts: %w", op, m.retryMaxAttempts, lastErr)
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
	if err := m.retryWithBackoff(ctx, "add", func(ctx context.Context) error {
		return coll.AddDocument(ctx, chromem.Document{
			ID:      key,
			Content: value,
		})
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
	var results []chromem.Result
	if err := m.retryWithBackoff(ctx, "query", func(ctx context.Context) error {
		var qErr error
		results, qErr = coll.Query(ctx, query, topK, nil, nil)
		return qErr
	}); err != nil {
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

func (m *LongTermMemory) SaveKnowledgeChunk(ctx context.Context, key, value string) error {
	log.Debug("→ LongTermMemory.SaveKnowledgeChunk(key=%s, len=%d)", key, len(value))
	coll, err := m.ensureKnowledgeCollection(ctx)
	if err != nil {
		return fmt.Errorf("chromem get knowledge collection: %w", err)
	}
	return m.retryWithBackoff(ctx, "add_knowledge", func(ctx context.Context) error {
		return coll.AddDocument(ctx, chromem.Document{
			ID:      key,
			Content: value,
		})
	})
}

func (m *LongTermMemory) RecallKnowledge(ctx context.Context, query string, topK int) ([]string, error) {
	log.Debug("→ LongTermMemory.RecallKnowledge(query=%s, topK=%d)", query, topK)
	coll := m.db.GetCollection("knowledge", m.embedFunc)
	if coll == nil {
		return []string{}, nil
	}
	var results []chromem.Result
	if err := m.retryWithBackoff(ctx, "query_knowledge", func(ctx context.Context) error {
		var qErr error
		results, qErr = coll.Query(ctx, query, topK, nil, nil)
		return qErr
	}); err != nil {
		return nil, fmt.Errorf("query knowledge chromem: %w", err)
	}
	chunks := make([]string, len(results))
	for i, res := range results {
		chunks[i] = res.Content
	}
	return chunks, nil
}

func (m *LongTermMemory) Close() error {
	return nil
}
