package scanner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
	"hybrid-solana-bot/internal/notifier"
	"hybrid-solana-bot/internal/orchestrator"
)

type TokenProfile struct {
	ChainId      string `json:"chainId"`
	TokenAddress string `json:"tokenAddress"`
}

type Scanner struct {
	cfg      config.Config
	orch     *orchestrator.Orchestrator
	mem      *memory.MemoryStore
	log      *zap.Logger
	seen     map[string]bool
	notifier *notifier.TelegramNotifier
}

func NewScanner(cfg config.Config, orch *orchestrator.Orchestrator, mem *memory.MemoryStore, log *zap.Logger) *Scanner {
	return &Scanner{
		cfg:      cfg,
		orch:     orch,
		mem:      mem,
		log:      log,
		seen:     make(map[string]bool),
		notifier: notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs),
	}
}

func (s *Scanner) Start() {
	s.log.Info("Starting automatic new token scanner")
	for {
		userCfg := s.mem.GetUserConfig()
		if !userCfg.AutoTrade {
			time.Sleep(5 * time.Second)
			continue
		}

		interval := userCfg.ScannerIntervalSec
		if interval < 5 {
			interval = 5 // minimum safety
		}
		
		s.scanLatest()
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

func (s *Scanner) scanLatest() {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/token-profiles/latest/v1")
	if err != nil {
		s.log.Error("Scanner failed to fetch latest profiles", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	var profiles []TokenProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		s.log.Error("Scanner failed to parse profiles", zap.Error(err))
		return
	}

	for _, p := range profiles {
		if p.ChainId == "solana" {
			if !s.seen[p.TokenAddress] {
				s.seen[p.TokenAddress] = true
				go s.processNewToken(p.TokenAddress)
			}
		}
	}
}

func (s *Scanner) processNewToken(token string) {
	s.log.Info("Scanner detected new Solana token", zap.String("token", token))

	// Give it a brief delay before fetching metrics, as liquidity might take a few seconds to appear
	time.Sleep(3 * time.Second)

	metricsData, err := metrics.FetchTokenMetrics(token)
	if err != nil {
		s.log.Debug("Skipping token, metrics not ready yet", zap.String("token", token))
		return
	}

	result := s.orch.Process(metricsData)
	
	// Convert result to map to check status
	resMap, ok := result.(map[string]interface{})
	if ok && resMap["status"] == "approved" {
		msg := fmt.Sprintf("🚨 *AUTOSCAN: New Token Approved!*\n*Token:* `%s`\n*Liquidity:* $%.2f\n*Result:* %+v", 
			token, metricsData.LiquidityUSD, result)
		if err := s.notifier.SendMessage(msg); err != nil {
			s.log.Error("Autoscan telegram failed", zap.Error(err))
		}
		s.log.Info("Autoscan approved token", zap.String("token", token))
	}
}
