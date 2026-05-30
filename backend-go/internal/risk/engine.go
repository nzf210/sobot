package risk

import (
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/models"
)

type RiskEngine struct {
	mem *memory.MemoryStore
}

func New(mem *memory.MemoryStore) *RiskEngine {
	return &RiskEngine{mem: mem}
}

func (r *RiskEngine) Validate(metrics models.TokenMetrics) bool {
	cfg := r.mem.GetUserConfig()

	// ── Liquidity gate ───────────────────────────────────────────────────────
	if metrics.LiquiditySOL < cfg.MinLiquiditySOL {
		return false
	}
	if cfg.MaxLiquiditySOL > 0 && metrics.LiquiditySOL > cfg.MaxLiquiditySOL {
		return false
	}

	// ── Volume gate ──────────────────────────────────────────────────────────
	if metrics.Volume5mSOL < cfg.MinVolumeSOL {
		return false
	}

	// ── Market cap gate ──────────────────────────────────────────────────────
	if metrics.MarketCapSOL < cfg.MinMcapSOL {
		return false
	}
	if cfg.MaxMcapSOL > 0 && metrics.MarketCapSOL > cfg.MaxMcapSOL {
		return false
	}

	// ── Quality gates ────────────────────────────────────────────────────────
	if metrics.OrganicScore < cfg.MinOrganicScore {
		return false
	}
	if metrics.WashTradeProbability > (cfg.MaxWashTradePct / 100.0) {
		return false
	}

	// ── Pair age gate (skip tokens older than 48h for sniper mode) ───────────
	// PairAgeSec==0 means DexScreener didn't return creation time — allow through
	if metrics.PairAgeSec > 0 && metrics.PairAgeSec > 48*3600 {
		return false
	}

	// ── Price change sanity (avoid rug-dumps already in progress) ────────────
	if metrics.PriceChange5m < -30.0 {
		return false // already dumping hard
	}

	return true
}