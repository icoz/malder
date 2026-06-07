package memory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/icoz/malder/internal/log"
	"go.etcd.io/bbolt"
)

type Provenance struct {
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	SourceURL string   `json:"source_url,omitempty"`
	Parents   []string `json:"parents,omitempty"`
	Preview   string   `json:"preview"`
	IsRaw     bool     `json:"is_raw"`
	Timestamp int64    `json:"timestamp"`
}

type SourceStore struct {
	db *bbolt.DB
}

func NewSourceStore(path string) (*SourceStore, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("bolt open: %w", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("provenance"))
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("bolt init: %w", err)
	}
	return &SourceStore{db: db}, nil
}

func (s *SourceStore) Close() error {
	return s.db.Close()
}

func (s *SourceStore) Put(p Provenance) error {
	p.Timestamp = time.Now().UnixNano()
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal provenance: %w", err)
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("provenance"))
		return b.Put([]byte(p.Key), data)
	})
}

func (s *SourceStore) Get(key string) (*Provenance, error) {
	var p Provenance
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("provenance"))
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("provenance not found: %s", key)
		}
		return json.Unmarshal(data, &p)
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *SourceStore) GetChain(key string) ([]Provenance, error) {
	var chain []Provenance
	seen := make(map[string]bool)

	var walk func(k string) error
	walk = func(k string) error {
		if seen[k] {
			return nil
		}
		seen[k] = true
		p, err := s.Get(k)
		if err != nil {
			return err
		}
		chain = append(chain, *p)
		for _, parent := range p.Parents {
			if err := walk(parent); err != nil {
				log.Warn("SourceStore: ошибка обхода родителя %s: %v", parent, err)
			}
		}
		return nil
	}

	if err := walk(key); err != nil {
		return chain, err
	}
	return chain, nil
}

func (s *SourceStore) GetSourceURLs(key string) []string {
	chain, err := s.GetChain(key)
	if err != nil {
		log.Warn("SourceStore: GetSourceURLs(%s) error: %v", key, err)
		return nil
	}
	var urls []string
	seen := make(map[string]bool)
	for _, p := range chain {
		if p.Kind == "page" && p.SourceURL != "" && !seen[p.SourceURL] {
			urls = append(urls, p.SourceURL)
			seen[p.SourceURL] = true
		}
	}
	return urls
}

func (s *SourceStore) ListByKind(kind string) ([]Provenance, error) {
	var results []Provenance
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("provenance"))
		return b.ForEach(func(k, v []byte) error {
			var p Provenance
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			if p.Kind == kind {
				results = append(results, p)
			}
			return nil
		})
	})
	return results, err
}
