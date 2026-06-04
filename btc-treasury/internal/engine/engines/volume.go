package engines

import (
	"math"

	"btc-treasury/internal/models"
)

type VolumeEngine struct{}

func (e VolumeEngine) IsVolumeSpike(metrics *models.PairMetrics) bool {
	return metrics.VolumeGrowth > 1.0 // volume_growth > 1.0 means > 2x average
}

func (e VolumeEngine) IsVolumeExpansion(metrics *models.PairMetrics) bool {
	return metrics.VolumeExpansion
}

func (e VolumeEngine) IsLiquidityGrowing(metrics *models.PairMetrics) bool {
	return metrics.LiquidityGrowth
}

func (e VolumeEngine) IsWashTrade(metrics *models.PairMetrics) bool {
	wideSpread := metrics.SpreadPct > 0.5   // > 0.5% spread
	lowMovement := math.Abs(metrics.RsScore) < 1.0 // barely moving
	highVolume := metrics.VolumeGrowth > 0.8  // volume picking up

	return wideSpread && lowMovement && highVolume
}

func (e VolumeEngine) IsPairEligible(metrics *models.PairMetrics) bool {
	return !metrics.LowLiquidity && !metrics.WideSpread && !e.IsWashTrade(metrics)
}

func (e VolumeEngine) ScoreComponent(metrics *models.PairMetrics) float64 {
	if !e.IsPairEligible(metrics) {
		return 0.0
	}

	var score float64

	// Volume spike (0-5 points)
	if e.IsVolumeSpike(metrics) {
		score += 5.0
	} else if metrics.VolumeGrowth > 0.5 {
		score += 3.0
	} else if metrics.VolumeGrowth > 0.0 {
		score += 1.0
	}

	// Volume expansion (0-3 points)
	if e.IsVolumeExpansion(metrics) {
		score += 3.0
	}

	// Liquidity growth (0-2 points)
	if e.IsLiquidityGrowing(metrics) {
		score += 2.0
	}

	if score < 0.0 {
		return 0.0
	}
	if score > 10.0 {
		return 10.0
	}
	return score
}
