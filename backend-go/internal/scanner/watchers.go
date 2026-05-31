package scanner

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// seenWithTTL is a thread-safe set with automatic TTL eviction.
type seenWithTTL struct {
	mu     sync.RWMutex
	items  map[string]time.Time
	ttl    time.Duration
	stopCh chan struct{}
}

func newSeenWithTTL(ttl time.Duration) *seenWithTTL {
	s := &seenWithTTL{
		items:  make(map[string]time.Time),
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

func (s *seenWithTTL) add(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[key]; exists {
		return false
	}
	s.items[key] = time.Now()
	return true
}

func (s *seenWithTTL) evictLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evict()
		case <-s.stopCh:
			return
		}
	}
}

func (s *seenWithTTL) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-s.ttl)
	for k, t := range s.items {
		if t.Before(cutoff) {
			delete(s.items, k)
		}
	}
}

// ── PumpFun / Solana Token Watcher ──────────────────────────────────────────
// Uses DexScreener token-profiles API to discover newly created Solana tokens.
// Token profiles include Pump.fun tokens as soon as they're listed on DexScreener.
// PumpFun tokens have no liquidity until they graduate to Raydium (~20 min).
// The scanner worker polls each pumpfun token for up to 30 min waiting for graduation.

type PumpFunWatcher struct {
	log     *zap.Logger
	seen    *seenWithTTL
	outChan chan<- string
}

func NewPumpFunWatcher(log *zap.Logger, out chan<- string) *PumpFunWatcher {
	return &PumpFunWatcher{
		log:     log,
		seen:    newSeenWithTTL(24 * time.Hour),
		outChan: out,
	}
}

func (w *PumpFunWatcher) Start() {
	w.log.Info("PumpFun/Solana Watcher started (using token-profiles)")
	backoff := time.Second
	for {
		err := w.fetch()
		if err != nil {
			w.log.Warn("PumpFun fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		} else {
			backoff = time.Second
			time.Sleep(15 * time.Second)
		}
	}
}

func (w *PumpFunWatcher) fetch() error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/token-profiles/latest/v1")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data []struct {
		ChainId      string `json:"chainId"`
		TokenAddress string `json:"tokenAddress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	for _, t := range data {
		if t.ChainId != "solana" {
			continue
		}
		if w.seen.add(t.TokenAddress) {
			w.log.Info("New Solana token detected", zap.String("token", t.TokenAddress))
			w.outChan <- t.TokenAddress
		}
	}
	return nil
}

// ── Raydium Watcher ─────────────────────────────────────────────────────────
// Uses DexScreener search API. Search returns top 30 results sorted by relevance,
// so the time window is wider (60 min) to catch newly indexed pairs.

type RaydiumWatcher struct {
	log     *zap.Logger
	seen    *seenWithTTL
	outChan chan<- string
}

func NewRaydiumWatcher(log *zap.Logger, out chan<- string) *RaydiumWatcher {
	return &RaydiumWatcher{
		log:     log,
		seen:    newSeenWithTTL(24 * time.Hour),
		outChan: out,
	}
}

func (w *RaydiumWatcher) Start() {
	w.log.Info("Raydium Watcher started")
	backoff := time.Second
	for {
		err := w.fetch()
		if err != nil {
			w.log.Warn("Raydium fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		} else {
			backoff = time.Second
			time.Sleep(45 * time.Second)
		}
	}
}

func (w *RaydiumWatcher) fetch() error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/latest/dex/search?q=raydium")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data struct {
		Pairs []struct {
			DexId         string `json:"dexId"`
			ChainId       string `json:"chainId"`
			BaseToken     struct {
				Address string `json:"address"`
			} `json:"baseToken"`
			PairCreatedAt int64  `json:"pairCreatedAt"`
			Liquidity     struct {
				Usd float64 `json:"usd"`
			} `json:"liquidity"`
		} `json:"pairs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	cutoff := time.Now().Add(-90 * time.Minute)
	for _, p := range data.Pairs {
		if p.ChainId != "solana" || p.DexId != "raydium" {
			continue
		}
		if p.PairCreatedAt == 0 {
			continue
		}
		if p.Liquidity.Usd < 1000 {
			continue
		}
		if time.UnixMilli(p.PairCreatedAt).Before(cutoff) {
			continue
		}
		if w.seen.add(p.BaseToken.Address) {
			w.log.Info("Raydium new pool",
				zap.String("token", p.BaseToken.Address),
				zap.Float64("liquidity_usd", p.Liquidity.Usd))
			w.outChan <- p.BaseToken.Address
		}
	}
	return nil
}

// ── Meteora Watcher ─────────────────────────────────────────────────────────

type MeteoraWatcher struct {
	log     *zap.Logger
	seen    *seenWithTTL
	outChan chan<- string
}

func NewMeteoraWatcher(log *zap.Logger, out chan<- string) *MeteoraWatcher {
	return &MeteoraWatcher{
		log:     log,
		seen:    newSeenWithTTL(24 * time.Hour),
		outChan: out,
	}
}

func (w *MeteoraWatcher) Start() {
	w.log.Info("Meteora Watcher started")
	backoff := time.Second
	for {
		err := w.fetch()
		if err != nil {
			w.log.Warn("Meteora fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		} else {
			backoff = time.Second
			time.Sleep(45 * time.Second)
		}
	}
}

func (w *MeteoraWatcher) fetch() error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/latest/dex/search?q=meteora")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data struct {
		Pairs []struct {
			DexId     string `json:"dexId"`
			ChainId   string `json:"chainId"`
			BaseToken struct {
				Address string `json:"address"`
			} `json:"baseToken"`
			PairCreatedAt int64 `json:"pairCreatedAt"`
			Liquidity     struct {
				Usd float64 `json:"usd"`
			} `json:"liquidity"`
		} `json:"pairs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	cutoff := time.Now().Add(-120 * time.Minute)
	for _, p := range data.Pairs {
		if p.ChainId != "solana" || p.DexId != "meteora" {
			continue
		}
		if p.PairCreatedAt == 0 {
			continue
		}
		if p.Liquidity.Usd < 500 {
			continue
		}
		if time.UnixMilli(p.PairCreatedAt).Before(cutoff) {
			continue
		}
		if w.seen.add(p.BaseToken.Address) {
			w.log.Info("Meteora new pool", zap.String("token", p.BaseToken.Address))
			w.outChan <- p.BaseToken.Address
		}
	}
	return nil
}