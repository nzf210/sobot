package risk

import "hybrid-solana-bot/internal/models"

type RiskEngine struct {}

func New() *RiskEngine {
    return &RiskEngine{}
}

func (r *RiskEngine) Validate(metrics models.TokenMetrics) bool {

    if metrics.LiquidityUSD < 10000 {
        return false
    }

    if metrics.WashTradeProbability > 0.7 {
        return false
    }

    return true
}