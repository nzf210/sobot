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

	// Wait for DexScreener to index the new pair (PumpFun tokens can take 10-30s to appear)
	time.Sleep(10 * time.Second)

	// Retry up to 3 times with backoff for tokens that return zero liquidity
	var metricsData models.TokenMetrics
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		metricsData, err = metrics.FetchTokenMetrics(token)
		if err == nil && metricsData.LiquidityUSD > 0 {
			break
		}
		if attempt < 2 {
			s.log.Debug("Token metrics not ready, retrying",
				zap.String("token", token),
				zap.Int("attempt", attempt+1),
			)
			time.Sleep(time.Duration(5*(attempt+1)) * time.Second)
		}
	}
	if err != nil {
		s.log.Debug("Skipping token, metrics not available", zap.String("token", token), zap.Error(err))
		s.stats.AddResult(token, false, "Metrics not available", 0)
		return
	}
	if metricsData.LiquidityUSD <= 0 {
		s.log.Debug("Skipping token, zero liquidity", zap.String("token", token))
		s.stats.AddResult(token, false, "Zero liquidity", 0)
		return
	}

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
