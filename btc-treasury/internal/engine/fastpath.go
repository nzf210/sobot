package engine

import (
	"fmt"
	"math"
	"time"

	"btc-treasury/internal/models"
)

func QuantFastPath(
	data *models.BtcMarketData,
	treasury *models.BtcTreasuryState,
	opportunity float64,
	riskLevel string,
	marketRegime string,
	lossStreak int,
	takerFeePct float64,
) *models.FullBtcAdvisory {

	// Danger regimes first
	if marketRegime == "LOW_LIQUIDITY_DANGER" || marketRegime == "HIGH_VOLATILITY_DANGER" || marketRegime == "PANIC_SELLOFF" {
		mode := "SAFE_MODE"
		adv := QuantAdvisory(data, marketRegime, "CRITICAL", nil, opportunity, mode, takerFeePct)
		return &adv
	}

	// Loss streak
	if lossStreak >= 3 {
		mode := TreasuryMode(data, treasury, "HIGH")
		adv := QuantAdvisory(data, marketRegime, "HIGH", []string{"Loss streak >= 3"}, opportunity, mode, takerFeePct)
		return &adv
	}

	// Clear rejection zone
	if opportunity < 50.0 && riskLevel != "LOW" {
		mode := TreasuryMode(data, treasury, riskLevel)
		adv := QuantAdvisory(data, marketRegime, riskLevel, nil, opportunity, mode, takerFeePct)
		return &adv
	}

	// FAKE_BREAKOUT / CHOPPY / DISTRIBUTION
	if marketRegime == "FAKE_BREAKOUT" || marketRegime == "CHOPPY" || marketRegime == "DISTRIBUTION" {
		mode := TreasuryMode(data, treasury, riskLevel)
		effRisk := riskLevel
		if riskLevel == "LOW" {
			effRisk = "MEDIUM"
		}
		adv := QuantAdvisory(data, marketRegime, effRisk, nil, opportunity, mode, takerFeePct)
		return &adv
	}

	// TRENDING_BEARISH
	if marketRegime == "TRENDING_BEARISH" {
		mode := "REDUCE_RISK"
		adv := QuantAdvisory(data, marketRegime, "HIGH", nil, opportunity, mode, takerFeePct)
		return &adv
	}

	// Strong quant signal: approve without LLM
	if riskLevel == "LOW" && opportunity >= 80.0 && data.Confidence >= 0.85 {
		mode := TreasuryMode(data, treasury, riskLevel)
		adv := QuantAdvisory(data, marketRegime, riskLevel, nil, opportunity, mode, takerFeePct)
		return &adv
	}

	// MEDIUM risk + clear non-approval
	if riskLevel == "MEDIUM" && opportunity < 70.0 {
		mode := TreasuryMode(data, treasury, riskLevel)
		adv := QuantAdvisory(data, marketRegime, riskLevel, nil, opportunity, mode, takerFeePct)
		return &adv
	}

	return nil
}

func ClassifyRegime(data *models.BtcMarketData) string {
	if data.LiquidityScore < 3.0 && data.VolumeScore < 3.0 {
		return "LOW_LIQUIDITY_DANGER"
	}
	if data.VolatilityScore > 9.0 {
		return "HIGH_VOLATILITY_DANGER"
	}
	if data.TrendStrength < -8.0 && data.VolatilityScore > 7.0 {
		return "PANIC_SELLOFF"
	}
	if data.TrendStrength > 7.0 && data.VolumeScore > 6.0 && data.BreakoutProbability > 0.6 {
		return "TRENDING_BULLISH"
	}
	if data.TrendStrength < -7.0 && data.VolumeScore > 6.0 {
		return "TRENDING_BEARISH"
	}
	if data.ReversalProbability > 0.75 && data.TrendStrength > 5.0 {
		return "FAKE_BREAKOUT"
	}
	if data.BreakoutProbability > 0.75 && data.TrendStrength > 0.0 {
		return "BREAKOUT_EXPANSION"
	}
	if data.TrendStrength < -3.0 && data.VolumeScore > 5.0 {
		return "DISTRIBUTION"
	}
	if data.TrendStrength > 3.0 && data.VolumeScore < 5.0 && data.BreakoutProbability < 0.35 {
		return "ACCUMULATION"
	}
	if (data.Confidence < 0.4 && data.VolumeScore < 4.0) || (math.Abs(data.TrendStrength) < 2.0 && data.Confidence < 0.35) {
		return "CHOPPY"
	}
	if math.Abs(data.TrendStrength) < 3.0 && data.VolumeScore > 3.0 {
		return "RANGING"
	}
	return "RANGING"
}

func AssessRisk(data *models.BtcMarketData, treasury *models.BtcTreasuryState, lossStreak int) (string, []string) {
	var riskScore float64
	var warnings []string

	if data.LiquidityScore < 4.0 {
		riskScore += 3.0
		warnings = append(warnings, "Liquidity critically low")
	}
	if data.SpreadScore < 4.0 {
		riskScore += 3.0
		warnings = append(warnings, "Spread critically wide")
	}
	if data.VolatilityScore > 9.0 {
		riskScore += 3.0
		warnings = append(warnings, "Extreme volatility")
	}
	if data.DailyDrawdown > 0.05 {
		riskScore += 3.0
		warnings = append(warnings, "Daily drawdown exceeding 5%")
	} else if data.DailyDrawdown > 0.03 {
		riskScore += 2.0
		warnings = append(warnings, "Daily drawdown exceeding 3%")
	}
	if lossStreak >= 3 {
		riskScore += 2.0
		warnings = append(warnings, fmt.Sprintf("Loss streak: %d consecutive losses", lossStreak))
	}
	if data.Confidence < 0.5 {
		riskScore += 1.0
		warnings = append(warnings, "Low confidence signal")
	}
	if data.ReversalProbability > 0.6 {
		riskScore += 2.0
		warnings = append(warnings, "High reversal probability")
	}
	if treasury.BtcGrowth7d < -0.05 {
		riskScore += 1.0
		warnings = append(warnings, "7-day BTC treasury decline")
	}
	if data.PortfolioExposure > 0.40 {
		riskScore += 1.0
		warnings = append(warnings, "Portfolio exposure above 40%")
	}

	level := "LOW"
	if riskScore >= 7.0 {
		level = "CRITICAL"
	} else if riskScore >= 4.0 {
		level = "HIGH"
	} else if riskScore >= 2.0 {
		level = "MEDIUM"
	} else {
		if len(warnings) == 0 {
			warnings = append(warnings, "No significant risk factors")
		}
	}

	return level, warnings
}

func TreasuryMode(data *models.BtcMarketData, treasury *models.BtcTreasuryState, riskLevel string) string {
	if riskLevel == "CRITICAL" {
		return "SAFE_MODE"
	}
	if data.LiquidityScore < 4.0 || data.SpreadScore < 4.0 || data.VolatilityScore > 9.0 {
		return "SAFE_MODE"
	}
	if riskLevel == "HIGH" {
		return "REDUCE_RISK"
	}
	if data.DailyDrawdown > 0.04 || treasury.BtcGrowth7d < -0.03 {
		return "PROTECT"
	}
	if data.TrendStrength > 4.0 && data.Confidence > 0.65 && riskLevel == "LOW" {
		return "ACCUMULATE"
	}
	if data.TrendStrength > 2.0 && riskLevel == "MEDIUM" {
		return "ACCUMULATE"
	}
	return "PROTECT"
}

func OpportunityScore(data *models.BtcMarketData) float64 {
	trendNorm := math.Max(0.0, math.Min(1.0, (data.TrendStrength+10.0)/20.0))

	score := data.LiquidityScore*0.20 +
		data.SpreadScore*0.10 +
		(10.0-data.VolatilityScore)*0.15 +
		data.VolumeScore*0.15 +
		trendNorm*10.0*0.20 +
		data.BreakoutProbability*10.0*0.15 +
		(1.0-data.ReversalProbability)*10.0*0.05

	return math.Round(score*10.0*10.0) / 10.0
}

func ShouldActivateLLM(
	opportunity float64,
	data *models.BtcMarketData,
	riskLevel string,
	marketRegime string,
	cfg *models.BtcConfig,
) bool {
	if opportunity >= 60.0 && opportunity < 80.0 {
		return true
	}
	if data.Confidence < cfg.LlmActivationThreshold {
		return true
	}
	if data.DailyDrawdown > 0.03 {
		return true
	}
	if (data.VolatilityScore > cfg.SafeModeVolatility || data.LiquidityScore < 4.0) &&
		(marketRegime == "TRENDING_BULLISH" ||
			marketRegime == "TRENDING_BEARISH" ||
			marketRegime == "BREAKOUT_EXPANSION" ||
			marketRegime == "ACCUMULATION") {
		return true
	}
	if riskLevel == "MEDIUM" && opportunity >= 70.0 && opportunity < 80.0 {
		return true
	}
	return false
}

func QuantAdvisory(
	data *models.BtcMarketData,
	regime string,
	riskLevel string,
	warnings []string,
	opportunity float64,
	treasuryMode string,
	takerFeePct float64,
) models.FullBtcAdvisory {
	roundTripFeePct := takerFeePct * 200.0 // e.g. 0.001 * 200 = 0.2%

	var tp, sl float64
	var tpReason, slReason string

	switch regime {
	case "HIGH_VOLATILITY_DANGER", "PANIC_SELLOFF", "LOW_LIQUIDITY_DANGER":
		sl = -math.Max(2.5, roundTripFeePct+2.0)
		tp = 7.5
		tpReason = "Danger regime — wide TP needed if position must be held"
		slReason = fmt.Sprintf("Danger regime — %.1f%% SL + %.1f%% fee", math.Abs(sl), roundTripFeePct)
	case "TRENDING_BULLISH", "BREAKOUT_EXPANSION":
		baseSl := math.Max(1.5, roundTripFeePct+0.8)
		slVal := baseSl
		if slVal > 2.0 {
			slVal = 2.0
		}
		sl = -slVal
		if data.Confidence >= 0.85 {
			tp = 7.0
		} else {
			tp = 5.5
		}
		tpReason = fmt.Sprintf("TRENDING regime — %.1f%% TP captures momentum above resistance", tp)
		slReason = fmt.Sprintf("TRENDING SL %.1f%% + %.1f%% fee = %.1f%% max loss", baseSl, roundTripFeePct, baseSl+roundTripFeePct)
	case "RANGING", "ACCUMULATION":
		baseSl := math.Max(1.0, roundTripFeePct+0.8)
		slVal := baseSl
		if slVal > 1.5 {
			slVal = 1.5
		}
		sl = -slVal
		if opportunity >= 75.0 {
			tp = 4.0
		} else {
			tp = 3.0
		}
		tpReason = fmt.Sprintf("RANGING/ACCUMULATION — %.1f%% TP for sideways breakout", tp)
		slReason = fmt.Sprintf("CALM SL %.1f%% + %.1f%% fee = %.1f%% max loss", baseSl, roundTripFeePct, baseSl+roundTripFeePct)
	default:
		if data.VolatilityScore >= 7.0 {
			baseSl := math.Max(2.0, roundTripFeePct+1.5)
			slVal := baseSl
			if slVal > 2.5 {
				slVal = 2.5
			}
			sl = -slVal
			if data.Confidence >= 0.80 {
				tp = 8.0
			} else {
				tp = 6.0
			}
			tpReason = fmt.Sprintf("VOLATILE — %.1f%% TP for high-ATR environment", tp)
			slReason = fmt.Sprintf("VOLATILE SL %.1f%% + %.1f%% fee = %.1f%% max loss", baseSl, roundTripFeePct, baseSl+roundTripFeePct)
		} else {
			baseSl := math.Max(0.8, roundTripFeePct+0.6)
			slVal := baseSl
			if slVal < 0.8 {
				slVal = 0.8
			} else if slVal > 2.0 {
				slVal = 2.0
			}
			sl = -slVal
			tp = 5.5
			tpReason = fmt.Sprintf("Quant fallback: 5.5%s TP (regime: %s)", "%", regime)
			slReason = fmt.Sprintf("Quant fallback: %.1f%% SL + %.1f%% fee = %.1f%% max loss", baseSl, roundTripFeePct, baseSl+roundTripFeePct)
		}
	}

	recommendation := "REJECT"
	reason := "Weak opportunity — no trade is better than a low-confidence trade."

	switch riskLevel {
	case "CRITICAL":
		recommendation = "ENABLE_SAFE_MODE"
		reason = "CRITICAL risk level — treasury protection activated."
	case "HIGH":
		recommendation = "PROTECT_TREASURY"
		reason = "HIGH risk detected — prioritize treasury protection."
	case "MEDIUM":
		if opportunity > 60.0 {
			recommendation = "MONITOR"
			reason = "Medium risk with acceptable opportunity score. Monitor for improvement."
		} else {
			recommendation = "REDUCE_EXPOSURE"
			reason = "Medium risk with low opportunity. Reduce exposure."
		}
	default:
		if opportunity >= 75.0 && data.Confidence >= 0.80 {
			recommendation = "APPROVE"
			reason = "High opportunity score with strong confidence."
		} else if opportunity >= 60.0 {
			recommendation = "MONITOR"
			reason = "Opportunity meets baseline. Monitor for confirmation."
		} else if opportunity < 50.0 {
			recommendation = "PROTECT_TREASURY"
			reason = "Low risk but unactionable opportunity. Preserve treasury."
		}
	}

	return models.FullBtcAdvisory{
		Recommendation:    recommendation,
		Confidence:        data.Confidence,
		RiskLevel:         riskLevel,
		TreasuryMode:      treasuryMode,
		Reason:            reason,
		Warnings:          warnings,
		MarketRegime:      regime,
		OpportunityScore:  opportunity,
		BypassQuant:       false,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		DynamicTakeProfit: tp,
		DynamicStopLoss:   sl,
		TpReason:          tpReason,
		SlReason:          slReason,
	}
}
