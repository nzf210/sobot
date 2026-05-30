package scanner

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/notifier"
)

type TokenResult struct {
	Token      string
	Approved   bool
	Reason     string
	Confidence float64
	Timestamp  time.Time
}

type ScanStats struct {
	Scanned   atomic.Int64
	Passed    atomic.Int64
	Rejected  atomic.Int64
	results   []TokenResult
	mu        sync.Mutex
}

func NewScanStats() *ScanStats {
	return &ScanStats{
		results: make([]TokenResult, 0),
	}
}

func (s *ScanStats) AddResult(token string, approved bool, reason string, confidence float64) {
	s.Scanned.Add(1)
	if approved {
		s.Passed.Add(1)
	} else {
		s.Rejected.Add(1)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, TokenResult{
		Token:      token,
		Approved:   approved,
		Reason:     reason,
		Confidence: confidence,
		Timestamp:  time.Now(),
	})
}

func (s *ScanStats) GetAndReset() (int64, int64, int64, []TokenResult) {
	scanned := s.Scanned.Swap(0)
	passed := s.Passed.Swap(0)
	rejected := s.Rejected.Swap(0)

	s.mu.Lock()
	results := s.results
	s.results = make([]TokenResult, 0)
	s.mu.Unlock()

	return scanned, passed, rejected, results
}

type Reporter struct {
	stats    *ScanStats
	mem      *memory.MemoryStore
	notifier *notifier.TelegramNotifier
	log      *zap.Logger
	interval time.Duration
}

func NewReporter(stats *ScanStats, mem *memory.MemoryStore, notifier *notifier.TelegramNotifier, log *zap.Logger, intervalMinutes int) *Reporter {
	return &Reporter{
		stats:    stats,
		mem:      mem,
		notifier: notifier,
		log:      log,
		interval: time.Duration(intervalMinutes) * time.Minute,
	}
}

func (r *Reporter) Start() {
	r.log.Info("Starting periodic reporter", zap.Duration("interval", r.interval))
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for range ticker.C {
		r.report()
	}
}

func (r *Reporter) report() {
	scanned, passed, rejected, results := r.stats.GetAndReset()

	if scanned == 0 {
		return
	}

	r.log.Info("Scan report generated",
		zap.Int64("scanned", scanned),
		zap.Int64("passed", passed),
		zap.Int64("rejected", rejected))

	var msg string
	msg = fmt.Sprintf("📊 *Scan Report (5 menit terakhir)*\n\n")
	msg += fmt.Sprintf("*Total Scan:* %d token\n", scanned)
	msg += fmt.Sprintf("*✅ Lolos:* %d\n", passed)
	msg += fmt.Sprintf("*❌ Ditolak:* %d\n\n", rejected)

	if len(results) > 0 && len(results) <= 10 {
		msg += "*Detail Token:*\n"
		for i, res := range results {
			status := "❌"
			if res.Approved {
				status = "✅"
			}
			tokenShort := res.Token
			if len(tokenShort) > 8 {
				tokenShort = tokenShort[:8] + "..."
			}
			msg += fmt.Sprintf("%d. %s `%s`", i+1, status, tokenShort)
			if res.Confidence > 0 {
				msg += fmt.Sprintf(" (%.0f%%)", res.Confidence*100)
			}
			if res.Reason != "" && !res.Approved {
				msg += fmt.Sprintf("\n   _%s_", res.Reason)
			}
			msg += "\n"
		}
	}

	if err := r.notifier.SendMessage(msg); err != nil {
		r.log.Error("Failed to send report", zap.Error(err))
	}

	for _, res := range results {
		if !res.Approved && res.Reason != "" {
			tokenShort := res.Token
			if len(tokenShort) > 12 {
				tokenShort = tokenShort[:12] + "..."
			}
			lesson := fmt.Sprintf("[%s] Token %s ditolak karena: %s", res.Timestamp.Format("2006-01-02 15:04"), tokenShort, res.Reason)
			if err := r.mem.AddLesson(lesson); err != nil {
				r.log.Error("Failed to save lesson", zap.Error(err), zap.String("token", res.Token))
			}
		}
	}
}
