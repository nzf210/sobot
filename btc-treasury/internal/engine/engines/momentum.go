package engines

import (
	"btc-treasury/internal/models"
)

type MomentumEngine struct{}

func (e MomentumEngine) IsEmaBullishAligned(metrics *models.PairMetrics) bool {
	if metrics.Ema20 == 0.0 || metrics.Ema50 == 0.0 || metrics.Ema200 == 0.0 {
		return false
	}
	return metrics.Ema20 > metrics.Ema50 && metrics.Ema50 > metrics.Ema200
}

func (e MomentumEngine) IsEmaBearishAligned(metrics *models.PairMetrics) bool {
	if metrics.Ema20 == 0.0 || metrics.Ema50 == 0.0 || metrics.Ema200 == 0.0 {
		return false
	}
	return metrics.Ema20 < metrics.Ema50 && metrics.Ema50 < metrics.Ema200
}

func (e MomentumEngine) IsMacdBullish(metrics *models.PairMetrics) bool {
	return metrics.MacdLine > metrics.MacdSignal && metrics.MacdHistogram > 0.0
}

func (e MomentumEngine) IsMacdBearish(metrics *models.PairMetrics) bool {
	return metrics.MacdLine < metrics.MacdSignal && metrics.MacdHistogram < 0.0
}

func (e MomentumEngine) IsVolumeAboveAverage(metrics *models.PairMetrics) bool {
	return metrics.VolumeGrowth > 0.0
}

func (e MomentumEngine) IsAtrExpanding(metrics *models.PairMetrics) bool {
	return metrics.AtrExpansion > 0.15 // 15% expansion threshold
}

func (e MomentumEngine) ScoreTrendComponent(metrics *models.PairMetrics) float64 {
	var score float64

	// EMA alignment (0-4 points)
	if e.IsEmaBullishAligned(metrics) {
		score += 4.0
	} else if e.IsEmaBearishAligned(metrics) {
		score -= 2.0 // bearish = negative for long bias
	}

	// MACD (0-3 points)
	if e.IsMacdBullish(metrics) {
		score += 3.0
	} else if e.IsMacdBearish(metrics) {
		score -= 1.0
	}

	// RSI (0-2 points)
	if metrics.Rsi14 >= 40.0 && metrics.Rsi14 <= 60.0 {
		score += 2.0 // ideal range for continuation
	} else if metrics.Rsi14 > 60.0 && metrics.Rsi14 <= 70.0 {
		score += 1.0
	} else if metrics.Rsi14 < 40.0 {
		score += 1.0 // oversold can mean bounce potential
	}

	// ATR expansion (0-1 point)
	if e.IsAtrExpanding(metrics) {
		score += 1.0
	}

	// Clamp to [0, 10]
	if score < 0.0 {
		return 0.0
	}
	if score > 10.0 {
		return 10.0
	}
	return score
}

func (e MomentumEngine) ScoreVolatilityComponent(metrics *models.PairMetrics) float64 {
	refPrice := 0.0
	if metrics.Close15m > 0.0 {
		refPrice = metrics.Close15m
	} else if metrics.Close1h > 0.0 {
		refPrice = metrics.Close1h
	} else {
		return 5.0 // neutral fallback
	}

	atrPct := 0.0
	if refPrice > 0.0 {
		atrPct = metrics.Atr14 / refPrice
	}

	if atrPct >= 0.005 && atrPct <= 0.04 {
		return 10.0
	} else if atrPct > 0.04 && atrPct <= 0.08 {
		return 7.0
	} else if atrPct > 0.003 && atrPct < 0.005 {
		return 8.0
	} else if atrPct < 0.003 && atrPct > 0.001 {
		return 4.0
	} else if atrPct <= 0.001 {
		return 2.0
	} else {
		return 3.0 // > 8% ATR — too volatile for 1% risk framework
	}
}
