package scanner

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
	"hybrid-solana-bot/internal/models"
	"hybrid-solana-bot/internal/notifier"
	"hybrid-solana-bot/internal/orchestrator"
)

type Scanner struct {
	cfg         config.Config
	orch        *orchestrator.Orchestrator
	mem         *memory.MemoryStore
	log         *zap.Logger
	seen        *seenWithTTL
	notifier    *notifier.TelegramNotifier
	tokenChan   chan string
	pumpWatcher *PumpFunWatcher
	raydWatcher *RaydiumWatcher
	meteWatcher *MeteoraWatcher
	stats       *ScanStats
	reporter    *Reporter
}

func NewScanner(cfg config.Config, orch *orchestrator.Orchestrator, mem *memory.MemoryStore, log *zap.Logger) *Scanner {
	tokenChan := make(chan string, 100)
	tgNotifier := notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs)
	stats := NewScanStats()

	s := &Scanner{
		cfg:         cfg,
		orch:        orch,
		mem:         mem,
		log:         log,
		seen:        newSeenWithTTL(24 * time.Hour),
		notifier:    tgNotifier,
		tokenChan:   tokenChan,
		pumpWatcher: NewPumpFunWatcher(log, tokenChan),
		raydWatcher: NewRaydiumWatcher(log, tokenChan),
		meteWatcher: NewMeteoraWatcher(log, tokenChan),
		stats:       stats,
		reporter:    NewReporter(stats, mem, tgNotifier, log, 5),
	}

	// Start worker pool (4 workers)
	for i := 0; i < 4; i++ {
		go s.worker()
	}

	return s
}

func (s *Scanner) Start() {
	s.log.Info("Starting multi-source token scanner (Pump.fun, Raydium, Meteora)")

	go s.pumpWatcher.Start()
	go s.raydWatcher.Start()
	go s.meteWatcher.Start()
	go s.reporter.Start()
}

func (s *Scanner) worker() {
	for token := range s.tokenChan {
		if s.seen.add(token) {
			s.processNewToken(token)
		}
	}
}

func (s *Scanner) processNewToken(token string) {
	s.log.Info("Scanner detected new Solana token", zap.String("token", token))

	// Quick fetch — if liquidity > 0, process immediately
	metricsData, err := metrics.FetchTokenMetrics(token)
	if err == nil && metricsData.LiquidityUSD > 0 {
		s.process(token, metricsData)
		return
	}

	// PumpFun tokens have zero liquidity until they graduate to Raydium (~20 min).
	// Queue for background graduation polling instead of blocking a worker.
	if err != nil || metricsData.LiquidityUSD <= 0 {
		s.log.Debug("Token has no liquidity, queuing for graduation polling",
			zap.String("token", token),
		)
		s.stats.AddResult(token, false, "Zero liquidity (pending graduation)", 0)
		go s.pollGraduation(token)
	}
}

func (s *Scanner) pollGraduation(token string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	deadline := time.After(30 * time.Minute)

	for {
		select {
		case <-deadline:
			s.log.Debug("Token graduation timeout", zap.String("token", token))
			return
		case <-ticker.C:
			metricsData, err := metrics.FetchTokenMetrics(token)
			if err != nil {
				continue
			}
			if metricsData.LiquidityUSD > 0 {
				s.log.Info("Token graduated — processing", zap.String("token", token))
				s.process(token, metricsData)
				return
			}
		}
	}
}

func (s *Scanner) process(token string, metricsData models.TokenMetrics) {

	result := s.orch.Process(metricsData)

	if result.Approved {
		s.stats.AddResult(token, true, "", result.ConfidenceScore)
		msg := fmt.Sprintf("🚨 *AUTOSCAN: New Token Approved!*\n*Token:* `%s`\n*Confidence:* %.0f%%\n*Size:* %.4f SOL\n*LLM:* %s\n*Liquidity:* $%.2f",
			token, result.ConfidenceScore*100, result.RecommendedSizeSOL, result.LLMDecision, metricsData.LiquidityUSD)
		if err := s.notifier.SendMessage(msg); err != nil {
			s.log.Error("Autoscan telegram failed", zap.Error(err))
		}
		s.log.Info("Autoscan approved token", zap.String("token", token))
	} else if result.RejectedBy != "" {
		s.stats.AddResult(token, false, result.RejectedBy, result.ConfidenceScore)
		s.log.Info("Autoscan rejected token",
			zap.String("token", token),
			zap.String("reason", result.RejectedBy))
	}
}
