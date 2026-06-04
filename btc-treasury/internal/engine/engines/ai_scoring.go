package engines

import (
	"math"
	"sort"

	"btc-treasury/internal/models"
)

type AIScoringEngine struct{}

func (e AIScoringEngine) ScorePair(metrics *models.PairMetrics, risk *models.RiskAssessment) models.AIScoringOutput {
	rsEng := RSEngine{}
	momEng := MomentumEngine{}
	volEng := VolumeEngine{}

	rs := rsEng.ScoreComponent(metrics)
	vol := volEng.ScoreComponent(metrics)
	trend := momEng.ScoreTrendComponent(metrics)
	volQuality := momEng.ScoreVolatilityComponent(metrics)

	structure := e.scoreMarketStructure(metrics)

	total := rs*0.40 + vol*0.25 + trend*0.20 + volQuality*0.10 + structure*0.05

	components := models.AIScoreComponents{
		RelativeStrength:  rs,
		VolumeGrowth:      vol,
		TrendStrength:     trend,
		VolatilityQuality: volQuality,
		MarketStructure:   structure,
	}

	recommendation := "REJECT"
	if total >= 8.0 {
		recommendation = "APPROVE"
	} else if total >= 6.0 {
		recommendation = "MONITOR"
	}

	scoreValue := math.Round(total*10.0) / 10.0

	return models.AIScoringOutput{
		Pair:       metrics.Pair,
		Score:      scoreValue,
		Components: components,
		RankedPositions: []models.RankedPair{
			{
				Pair:           metrics.Pair,
				Score:          total,
				RsScore:        rs,
				VolumeScore:    vol,
				TrendScore:     trend,
				RiskScore:      0.0,
				Recommendation: recommendation,
			},
		},
	}
}

func (e AIScoringEngine) scoreMarketStructure(metrics *models.PairMetrics) float64 {
	rsEng := RSEngine{}
	var score float64

	if metrics.Ema20 > 0.0 && metrics.Ema50 > 0.0 {
		score += 3.0
	}

	if rsEng.IsRSRising(metrics) {
		score += 4.0
	}

	if metrics.SpreadPct < 0.1 {
		score += 3.0
	} else if metrics.SpreadPct < 0.3 {
		score += 1.5
	}

	if score < 0.0 {
		return 0.0
	}
	if score > 10.0 {
		return 10.0
	}
	return score
}

func (e AIScoringEngine) RankPairs(
	scorings []models.AIScoringOutput,
	risk *models.RiskAssessment,
	minScore float64,
) []models.RankedPair {
	var ranked []models.RankedPair
	for _, s := range scorings {
		if s.Score >= minScore {
			recommendation := "REJECT"
			if s.Score >= 8.0 {
				recommendation = "APPROVE"
			} else if s.Score >= 6.0 {
				recommendation = "MONITOR"
			}

			riskScore := 4.0
			if risk.RiskLevel == "LOW" || risk.RiskLevel == "MEDIUM" {
				riskScore = 8.0
			}

			ranked = append(ranked, models.RankedPair{
				Pair:           s.Pair,
				Score:          s.Score,
				RsScore:        s.Components.RelativeStrength,
				VolumeScore:    s.Components.VolumeGrowth,
				TrendScore:     s.Components.TrendStrength,
				RiskScore:      riskScore,
				Recommendation: recommendation,
			})
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})

	return ranked
}
