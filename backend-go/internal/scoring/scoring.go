package scoring

import "hybrid-solana-bot/internal/models"

func Compute(metrics models.TokenMetrics) float64 {

    score := 0.0

    score += metrics.OrganicScore * 0.4
    score += metrics.BuySellRatio * 0.2

    if metrics.Volume5m > 10000 {
        score += 0.3
    }

    return score
}