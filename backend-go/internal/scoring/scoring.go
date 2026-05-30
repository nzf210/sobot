package scoring

import "hybrid-solana-bot/internal/models"

// Compute returns a confidence score 0.0–1.0 for a given token.
// Score >= 0.6 is considered high-quality for AI analysis.
func Compute(metrics models.TokenMetrics) float64 {
	score := 0.0

	// ── 1. Organic quality (0–0.30) ─────────────────────────────────────────
	// OrganicScore is 0–1 from fetcher heuristics
	score += (metrics.OrganicScore / 100.0) * 0.30

	// ── 2. Buy pressure signal (0–0.25) ─────────────────────────────────────
	// BSR between 1.2 and 5 is healthy accumulation
	bsr := metrics.BuySellRatio
	if bsr >= 1.2 && bsr <= 5.0 {
		score += 0.25
	} else if bsr > 1.0 {
		score += 0.10
	}

	// ── 3. Volume momentum (0–0.25) ──────────────────────────────────────────
	// 5m volume in SOL: >5 SOL=small, >20=medium, >50=hot
	vol := metrics.Volume5mSOL
	switch {
	case vol >= 50:
		score += 0.25
	case vol >= 20:
		score += 0.18
	case vol >= 5:
		score += 0.10
	}

	// ── 4. Liquidity sweet-spot bonus (0–0.10) ───────────────────────────────
	// Best snipe zone: 66–500 SOL liquidity (enough depth, not overexposed)
	liq := metrics.LiquiditySOL
	if liq >= 66 && liq <= 500 {
		score += 0.10
	} else if liq > 500 && liq <= 1000 {
		score += 0.05
	}

	// ── 5. Market cap sweet-spot (0–0.10) ────────────────────────────────────
	// Early movers: 1k–20k SOL mcap is prime territory
	mc := metrics.MarketCapSOL
	if mc >= 1000 && mc <= 20000 {
		score += 0.10
	} else if mc > 20000 && mc <= 66000 {
		score += 0.05
	}

	// ── Wash trade penalty ───────────────────────────────────────────────────
	if metrics.WashTradeProbability > 0.5 {
		score -= 0.20
	} else if metrics.WashTradeProbability > 0.3 {
		score -= 0.10
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}