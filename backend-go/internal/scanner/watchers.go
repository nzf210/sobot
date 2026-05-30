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

// ── PumpFun Watcher ─────────────────────────────────────────────────────────

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
	w.log.Info("PumpFun Watcher started")
	backoff := time.Second
	for {
		err := w.fetch()
		if err != nil {
			w.log.Debug("PumpFun fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		} else {
			backoff = time.Second
			time.Sleep(60 * time.Second)
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

	var profiles []struct {
		ChainId      string `json:"chainId"`
		TokenAddress string `json:"tokenAddress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return err
	}

	for _, p := range profiles {
		if p.ChainId == "solana" && w.seen.add(p.TokenAddress) {
			w.log.Info("PumpFun new token", zap.String("token", p.TokenAddress))
			w.outChan <- p.TokenAddress
		}
	}
	return nil
}

// ── Raydium Watcher ─────────────────────────────────────────────────────────

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
			w.log.Debug("Raydium fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		} else {
			backoff = time.Second
			time.Sleep(30 * time.Second)
		}
	}
}

func (w *RaydiumWatcher) fetch() error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/latest/dex/pairs/solana")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data struct {
		Pairs []struct {
			DexId     string `json:"dexId"`
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

	cutoff := time.Now().Add(-30 * time.Minute)
	for _, p := range data.Pairs {
		if p.DexId != "raydium" || p.Liquidity.Usd < 1000 {
			continue
		}
		if time.UnixMilli(p.PairCreatedAt).Before(cutoff) {
			continue
		}
		if w.seen.add(p.BaseToken.Address) {
			w.log.Info("Raydium new pool", zap.String("token", p.BaseToken.Address))
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
			w.log.Debug("Meteora fetch failed, backing off", zap.Duration("backoff", backoff), zap.Error(err))
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

	cutoff := time.Now().Add(-60 * time.Minute)
	for _, p := range data.Pairs {
		if p.ChainId != "solana" || p.Liquidity.Usd < 500 {
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
