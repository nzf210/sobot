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

    if metrics.LiquiditySOL < cfg.MinLiquiditySOL {
        return false
    }

	if cfg.MaxLiquiditySOL > 0 && metrics.LiquiditySOL > cfg.MaxLiquiditySOL {
		return false
	}

	if metrics.Volume5mSOL < cfg.MinVolumeSOL {
		return false
	}

	if metrics.MarketCapSOL < cfg.MinMcapSOL {
		return false
	}

	if cfg.MaxMcapSOL > 0 && metrics.MarketCapSOL > cfg.MaxMcapSOL {
		return false
	}

	if metrics.OrganicScore < cfg.MinOrganicScore {
		return false
	}

    if metrics.WashTradeProbability > (cfg.MaxWashTradePct / 100.0) {
        return false
    }

    return true
}