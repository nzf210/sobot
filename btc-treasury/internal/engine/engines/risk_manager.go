package engines

import (
	"math"
	"time"

	"btc-treasury/internal/models"
)

type RiskManager struct{}

func (rm RiskManager) Assess(
	treasury *models.BtcTreasuryState,
	activePositions int,
	lossStreak int,
	drawdownPct float64,
	capitalUsdt float64,
	cfg *models.BtcConfig,
) models.RiskAssessment {
	maxLossUsdt := capitalUsdt * cfg.RiskPerTradePct
	positionSizeUsdt := maxLossUsdt / math.Max(math.Abs(cfg.StopLossPct), 0.001)

	riskLevel := rm.ComputeRiskLevel(
		lossStreak,
		drawdownPct,
		activePositions,
		cfg.MaxPositions,
	)

	pauseTrading := lossStreak >= cfg.MaxConsecutiveLosses
	reducePosition := drawdownPct > 0.10

	canOpenNew := !pauseTrading &&
		activePositions < cfg.MaxPositions &&
		drawdownPct <= 0.10 &&
		rm.IsPauseExpired(treasury)

	return models.RiskAssessment{
		RiskPerTradePct:     cfg.RiskPerTradePct * 100.0,
		PositionSizeUsdt:    positionSizeUsdt,
		MaxLossUsdt:         maxLossUsdt,
		CurrentExposureUsdt: capitalUsdt * cfg.MaxExposure,
		ActivePositions:     activePositions,
		LossStreak:          lossStreak,
		DrawdownPct:         drawdownPct,
		RiskLevel:           riskLevel,
		PauseTrading:        pauseTrading,
		ReducePosition:      reducePosition,
		CanOpenNew:          canOpenNew,
	}
}

func (rm RiskManager) ComputeRiskLevel(
	lossStreak int,
	drawdownPct float64,
	activePositions int,
	maxPositions int,
) string {
	score := 0.0

	if lossStreak >= 3 {
		score += 3.0
	} else if lossStreak >= 2 {
		score += 1.5
	} else if lossStreak >= 1 {
		score += 0.5
	}

	if drawdownPct > 0.10 {
		score += 3.0
	} else if drawdownPct > 0.05 {
		score += 2.0
	} else if drawdownPct > 0.02 {
		score += 1.0
	}

	if activePositions >= maxPositions {
		score += 2.0
	}

	if score >= 6.0 {
		return "CRITICAL"
	} else if score >= 4.0 {
		return "HIGH"
	} else if score >= 2.0 {
		return "MEDIUM"
	}
	return "LOW"
}

func (rm RiskManager) IsPauseExpired(treasury *models.BtcTreasuryState) bool {
	if treasury.TradingPausedUntil == "" {
		return true
	}
	pausedUntil, err := time.Parse(time.RFC3339, treasury.TradingPausedUntil)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(pausedUntil.UTC())
}

func (rm RiskManager) CalcPositionSize(
	capital float64,
	entryPrice float64,
	stopLossPct float64,
	riskPct float64,
	takerFeePct float64,
) float64 {
	slDistance := math.Abs(stopLossPct) / 100.0
	roundTripFee := takerFeePct * 2.0
	totalRiskPerUnit := slDistance + roundTripFee
	if totalRiskPerUnit > 0.0 && entryPrice > 0.0 {
		return (capital * riskPct) / totalRiskPerUnit
	}
	return 0.0
}

func (rm RiskManager) ShouldPause(lossStreak int, maxLosses int) bool {
	return lossStreak >= maxLosses
}

func (rm RiskManager) ShouldReduce(drawdownPct float64, threshold float64) bool {
	return drawdownPct > threshold
}

func (rm RiskManager) MinSlFromAtr(closePrice float64, atr14 float64) float64 {
	if closePrice <= 0.0 || atr14 <= 0.0 {
		return -0.8 // fallback floor
	}
	atrPct := (atr14 / closePrice) * 100.0
	minWidth := math.Max(atrPct*1.5, 0.8)
	return -minWidth
}

func (rm RiskManager) ClampSl(originalSl float64, closePrice float64, atr14 float64) float64 {
	minSl := rm.MinSlFromAtr(closePrice, atr14)
	clamped := math.Min(originalSl, minSl)
	if clamped < -5.0 {
		return -5.0
	}
	return clamped
}
