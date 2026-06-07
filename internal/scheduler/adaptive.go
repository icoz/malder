package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/tool"
)

type AdaptiveScheduler struct {
	mu                 sync.Mutex
	maxConcurrent      int
	minConcurrent      int
	maxConcurrentLimit int
	targetLatency      time.Duration
	latencies          []time.Duration
	errorCount         int
	totalCount         int
	windowSize         int
	adjustmentInterval time.Duration
	lastAdjust         time.Time
	consecutive429     int
	last429Time        time.Time
}

type Config struct {
	InitialMax      int
	MinConcurrent   int
	MaxConcurrent   int
	TargetLatency   time.Duration
	WindowSize      int
	AdjustmentEvery time.Duration
}

func NewAdaptiveScheduler(cfg Config) *AdaptiveScheduler {
	if cfg.InitialMax <= 0 {
		cfg.InitialMax = 2
	}
	if cfg.MinConcurrent <= 0 {
		cfg.MinConcurrent = 1
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}
	if cfg.TargetLatency == 0 {
		cfg.TargetLatency = 2 * time.Second
	}
	if cfg.WindowSize == 0 {
		cfg.WindowSize = 10
	}
	if cfg.AdjustmentEvery == 0 {
		cfg.AdjustmentEvery = 30 * time.Second
	}
	return &AdaptiveScheduler{
		maxConcurrent:      cfg.InitialMax,
		minConcurrent:      cfg.MinConcurrent,
		maxConcurrentLimit: cfg.MaxConcurrent,
		targetLatency:      cfg.TargetLatency,
		windowSize:         cfg.WindowSize,
		adjustmentInterval: cfg.AdjustmentEvery,
		lastAdjust:         time.Now(),
		latencies:          make([]time.Duration, 0, cfg.WindowSize),
	}
}

func (s *AdaptiveScheduler) Record(duration time.Duration, err error) {
	log.Debug("→ AdaptiveScheduler.Record(duration=%v, err=%v)", duration, err)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCount++
	if err != nil {
		s.errorCount++
		if errors.Is(err, tool.ErrTooManyRequests) {
			s.consecutive429++
			s.last429Time = time.Now()
		} else {
			s.consecutive429 = 0
		}
	} else {
		s.consecutive429 = 0
	}
	s.latencies = append(s.latencies, duration)
	if len(s.latencies) > s.windowSize {
		s.latencies = s.latencies[1:]
	}
}

func (s *AdaptiveScheduler) GetMaxConcurrent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consecutive429 > 0 && time.Since(s.last429Time) < 10*time.Second {
		return 1
	}
	if time.Since(s.lastAdjust) >= s.adjustmentInterval {
		s.adjust()
		s.lastAdjust = time.Now()
	}
	return s.maxConcurrent
}

func (s *AdaptiveScheduler) adjust() {
	if len(s.latencies) == 0 {
		return
	}
	var sum time.Duration
	for _, d := range s.latencies {
		sum += d
	}
	avgLatency := sum / time.Duration(len(s.latencies))
	errorRate := float64(s.errorCount) / float64(s.totalCount+1)

	change := 0
	if avgLatency > s.targetLatency*2 || errorRate > 0.3 {
		change = -1
	} else if avgLatency < s.targetLatency/2 && errorRate < 0.05 {
		change = +1
	}
	if change != 0 {
		newVal := s.maxConcurrent + change
		if newVal < s.minConcurrent {
			newVal = s.minConcurrent
		}
		if newVal > s.maxConcurrentLimit {
			newVal = s.maxConcurrentLimit
		}
		if newVal != s.maxConcurrent {
			log.Info("Адаптивный планировщик: изменяем параллелизм с %d на %d (avg=%v, errors=%.2f%%)",
				s.maxConcurrent, newVal, avgLatency, errorRate*100)
			s.maxConcurrent = newVal
		}
	}
	s.errorCount = 0
	s.totalCount = 0
}

func (s *AdaptiveScheduler) WaitIfNeeded(ctx context.Context) (err error) {
	defer func() {
		log.Debug("← AdaptiveScheduler.WaitIfNeeded = %v", err)
	}()
	log.Debug("→ AdaptiveScheduler.WaitIfNeeded")
	s.mu.Lock()
	if s.consecutive429 > 0 {
		backoff := time.Duration(1<<uint(s.consecutive429-1)) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			return nil
		}
	}
	s.mu.Unlock()
	return nil
}
