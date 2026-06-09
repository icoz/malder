package memory

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/icoz/malder/internal/log"
	"go.etcd.io/bbolt"
)

type DocumentMeta struct {
	ID           string   `json:"id"`
	OriginalName string   `json:"original_name"`
	ContentType  string   `json:"content_type"`
	Size         int64    `json:"size"`
	ChunkCount   int      `json:"chunk_count"`
	ChunkIDs     []string `json:"chunk_ids,omitempty"`
	MarkdownPath string   `json:"markdown_path"`
	CreatedAt    int64    `json:"created_at"`
}

type KnowledgeStore struct {
	db       *bbolt.DB
	docsPath string
}

func NewKnowledgeStore(db *bbolt.DB, docsPath string) *KnowledgeStore {
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("knowledge_meta"))
		return err
	}); err != nil {
		log.Error("KnowledgeStore: не удалось создать bucket: %v", err)
	}
	if err := os.MkdirAll(docsPath, 0755); err != nil {
		log.Error("KnowledgeStore: не удалось создать docsPath %s: %v", docsPath, err)
	}
	return &KnowledgeStore{db: db, docsPath: docsPath}
}

func (ks *KnowledgeStore) Create(meta *DocumentMeta, fullMD string) (string, error) {
	if meta.ID == "" {
		meta.ID = uuid.New().String()
	}
	meta.CreatedAt = time.Now().UnixNano()

	mdPath := filepath.Join(ks.docsPath, meta.ID+".md")
	if err := os.WriteFile(mdPath, []byte(fullMD), 0644); err != nil {
		return "", fmt.Errorf("запись markdown: %w", err)
	}
	meta.MarkdownPath = mdPath

	data, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal meta: %w", err)
	}
	if err := ks.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("knowledge_meta"))
		return b.Put([]byte(meta.ID), data)
	}); err != nil {
		return "", fmt.Errorf("save meta: %w", err)
	}
	return meta.ID, nil
}

func (ks *KnowledgeStore) SaveChunkIDs(docID string, ids []string) error {
	return ks.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("knowledge_meta"))
		raw := b.Get([]byte(docID))
		if raw == nil {
			return fmt.Errorf("document not found: %s", docID)
		}
		var meta DocumentMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return err
		}
		meta.ChunkIDs = ids
		meta.ChunkCount = len(ids)
		data, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		return b.Put([]byte(docID), data)
	})
}

func (ks *KnowledgeStore) Get(docID string) (*DocumentMeta, error) {
	var meta DocumentMeta
	err := ks.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("knowledge_meta"))
		raw := b.Get([]byte(docID))
		if raw == nil {
			return fmt.Errorf("document not found: %s", docID)
		}
		return json.Unmarshal(raw, &meta)
	})
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

func (ks *KnowledgeStore) List() ([]*DocumentMeta, error) {
	var docs []*DocumentMeta
	err := ks.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("knowledge_meta"))
		return b.ForEach(func(k, v []byte) error {
			var meta DocumentMeta
			if err := json.Unmarshal(v, &meta); err != nil {
				return err
			}
			docs = append(docs, &meta)
			return nil
		})
	})
	return docs, err
}

func (ks *KnowledgeStore) GetMarkdown(docID string) (string, error) {
	meta, err := ks.Get(docID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(meta.MarkdownPath)
	if err != nil {
		return "", fmt.Errorf("read markdown: %w", err)
	}
	return string(data), nil
}

func (ks *KnowledgeStore) Delete(docID string) error {
	meta, err := ks.Get(docID)
	if err != nil {
		return err
	}
	if err := os.Remove(meta.MarkdownPath); err != nil && !os.IsNotExist(err) {
		log.Warn("KnowledgeStore: удаление markdown %s: %v", meta.MarkdownPath, err)
	}
	return ks.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("knowledge_meta"))
		return b.Delete([]byte(docID))
	})
}

func (ks *KnowledgeStore) ExportArchive(ctx context.Context, w io.Writer) error {
	docs, err := ks.List()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, meta := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, err := os.ReadFile(meta.MarkdownPath)
		if err != nil {
			log.Warn("KnowledgeStore: экспорт %s: %v", meta.ID, err)
			continue
		}
		baseName := meta.OriginalName
		if ext := filepath.Ext(baseName); ext != "" {
			baseName = baseName[:len(baseName)-len(ext)] + ".md"
		} else {
			baseName += ".md"
		}
		fw, err := zw.Create(baseName)
		if err != nil {
			return fmt.Errorf("zip create %s: %w", baseName, err)
		}
		if _, err := fw.Write(data); err != nil {
			return fmt.Errorf("zip write %s: %w", baseName, err)
		}
	}
	return nil
}
