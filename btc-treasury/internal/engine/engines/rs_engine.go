package engines

import (
	"btc-treasury/internal/models"
)

type RSEngine struct{}

func (e RSEngine) CalculateRS(metrics *models.PairMetrics) float64 {
	return metrics.Rs15m*0.10 +
		metrics.Rs1h*0.35 +
		metrics.Rs4h*0.30 +
		metrics.Rs1d*0.25
}

func (e RSEngine) IsRSRising(metrics *models.PairMetrics) bool {
	return metrics.Rs1h > metrics.Rs4h && metrics.Rs1h > 0.0
}

func (e RSEngine) ScoreComponent(metrics *models.PairMetrics) float64 {
	rs := e.CalculateRS(metrics)
	span := 3.0
	score := ((rs + span) / (2.0 * span)) * 10.0
	if score < 0.0 {
		return 0.0
	}
	if score > 10.0 {
		return 10.0
	}
	return score
}
