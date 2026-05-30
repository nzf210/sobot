package scanner

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
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
}

func NewScanner(cfg config.Config, orch *orchestrator.Orchestrator, mem *memory.MemoryStore, log *zap.Logger) *Scanner {
	tokenChan := make(chan string, 100)

	s := &Scanner{
		cfg:         cfg,
		orch:        orch,
		mem:         mem,
		log:         log,
		seen:        newSeenWithTTL(24 * time.Hour),
		notifier:    notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs),
		tokenChan:   tokenChan,
		pumpWatcher: NewPumpFunWatcher(log, tokenChan),
		raydWatcher: NewRaydiumWatcher(log, tokenChan),
		meteWatcher: NewMeteoraWatcher(log, tokenChan),
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

	time.Sleep(3 * time.Second)

	metricsData, err := metrics.FetchTokenMetrics(token)
	if err != nil {
		s.log.Debug("Skipping token, metrics not ready yet", zap.String("token", token))
		return
	}

	result := s.orch.Process(metricsData)

	if result.Approved {
		msg := fmt.Sprintf("🚨 *AUTOSCAN: New Token Approved!*\n*Token:* `%s`\n*Confidence:* %.0f%%\n*Size:* %.4f SOL\n*LLM:* %s\n*Liquidity:* $%.2f",
			token, result.ConfidenceScore*100, result.RecommendedSizeSOL, result.LLMDecision, metricsData.LiquidityUSD)
		if err := s.notifier.SendMessage(msg); err != nil {
			s.log.Error("Autoscan telegram failed", zap.Error(err))
		}
		s.log.Info("Autoscan approved token", zap.String("token", token))
	} else if result.RejectedBy != "" {
		s.log.Info("Autoscan rejected token",
			zap.String("token", token),
			zap.String("reason", result.RejectedBy))
	}
}
