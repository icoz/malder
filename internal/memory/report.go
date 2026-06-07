package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/icoz/malder/internal/log"
	"go.etcd.io/bbolt"
)

type ReportStatus string

const (
	ReportStatusInProgress ReportStatus = "in_progress"
	ReportStatusCompleted  ReportStatus = "completed"
	ReportStatusError      ReportStatus = "error"
)

type Report struct {
	ID          string       `json:"id"`
	Query       string       `json:"query"`
	Status      ReportStatus `json:"status"`
	ReportText  string       `json:"report_text,omitempty"`
	Error       string       `json:"error,omitempty"`
	SourceCount int          `json:"source_count"`
	SourceURLs  []string     `json:"source_urls"`
	CreatedAt   int64        `json:"created_at"`
	CompletedAt *int64       `json:"completed_at,omitempty"`
	DurationMs  int64        `json:"duration_ms"`
}

type ReportStore struct {
	db *bbolt.DB
}

func NewReportStore(db *bbolt.DB) *ReportStore {
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("reports"))
		return err
	}); err != nil {
		log.Error("ReportStore: не удалось создать bucket: %v", err)
	}
	return &ReportStore{db: db}
}

func (s *ReportStore) Create(query string) (string, error) {
	id := uuid.New().String()
	now := time.Now().UnixNano()
	r := &Report{
		ID:        id,
		Query:     query,
		Status:    ReportStatusInProgress,
		CreatedAt: now,
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	err = s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("reports"))
		return b.Put([]byte(id), data)
	})
	if err != nil {
		return "", fmt.Errorf("bolt put report: %w", err)
	}
	log.Info("ReportStore: создан отчёт %s, query=%q", id, query)
	return id, nil
}

func (s *ReportStore) Complete(id, reportText string, sourceURLs []string, duration time.Duration) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("reports"))
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("report not found: %s", id)
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("unmarshal report: %w", err)
		}
		r.Status = ReportStatusCompleted
		r.ReportText = reportText
		r.SourceURLs = sourceURLs
		r.SourceCount = len(sourceURLs)
		now := time.Now().UnixNano()
		r.CompletedAt = &now
		r.DurationMs = duration.Milliseconds()
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		return b.Put([]byte(id), data)
	})
}

func (s *ReportStore) Fail(id, errMsg string, duration time.Duration) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("reports"))
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("report not found: %s", id)
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("unmarshal report: %w", err)
		}
		r.Status = ReportStatusError
		r.Error = errMsg
		now := time.Now().UnixNano()
		r.CompletedAt = &now
		r.DurationMs = duration.Milliseconds()
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		return b.Put([]byte(id), data)
	})
}

func (s *ReportStore) Get(id string) (*Report, error) {
	var r Report
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("reports"))
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("report not found: %s", id)
		}
		return json.Unmarshal(data, &r)
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *ReportStore) List() ([]*Report, error) {
	var reports []*Report
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("reports"))
		return b.ForEach(func(k, v []byte) error {
			var r Report
			if err := json.Unmarshal(v, &r); err != nil {
				log.Warn("ReportStore: ошибка десериализации отчёта %s: %v", string(k), err)
				return nil
			}
			r.ReportText = ""
			reports = append(reports, &r)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].CreatedAt > reports[j].CreatedAt
	})
	return reports, nil
}
