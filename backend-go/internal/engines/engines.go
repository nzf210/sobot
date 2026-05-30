package engines

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MomentumEngine analyzes price and volume momentum across multiple timeframes.
type MomentumEngine struct{}

func NewMomentumEngine() *MomentumEngine {
	return &MomentumEngine{}
}

// Analyze computes momentum signals and writes them to the PipelineSignal.
func (e *MomentumEngine) Analyze(sig *PipelineSignal) {
	m := sig.Metrics

	// ── 1. Volume Acceleration ──────────────────────────────────────────────
	// Ratio of 5m volume vs expected 5m volume from 1h (1h/12)
	// >1 means 5m vol is above average → accelerating
	expected5m := m.Volume1h / 12.0
	volAccel := 0.0
	if expected5m > 0 {
		volAccel = m.Volume5mSOL / (expected5m / solPrice(m.LiquiditySOL, m.LiquidityUSD))
		if volAccel > 5 {
			volAccel = 5 // cap at 5x
		}
	}
	sig.VolumeAcceleration = volAccel

	// ── 2. Price Momentum Z-score ───────────────────────────────────────────
	// Normalized price change: how extreme the 5m move is vs 24h context
	priceZ := 0.0
	if m.PriceChange24h != 0 {
		// Simple heuristic: 5m change vs expected (24h/288)
		expectedChange := m.PriceChange24h / 288.0
		stdDev := absF(m.PriceChange24h) / 10.0
		if stdDev > 0 {
			priceZ = (m.PriceChange5m - expectedChange) / stdDev
		}
	}
	sig.PriceMomentumZ = priceZ

	// ── 3. Momentum Score (0–1) ─────────────────────────────────────────────
	score := 0.5 // neutral base

	// Positive price momentum
	if m.PriceChange5m > 5 && m.PriceChange5m <= 30 {
		score += 0.20 // healthy pump
	} else if m.PriceChange5m > 0 && m.PriceChange5m <= 5 {
		score += 0.10 // mild up
	} else if m.PriceChange5m < -10 {
		score -= 0.30 // dumping
	} else if m.PriceChange5m < 0 {
		score -= 0.10 // mild down
	}

	// Volume acceleration bonus
	if volAccel >= 2.0 {
		score += 0.20 // strong acceleration
	} else if volAccel >= 1.2 {
		score += 0.10 // moderate acceleration
	} else if volAccel < 0.5 {
		score -= 0.10 // decelerating
	}

	// 1h trend confirmation
	if m.PriceChange1h > 10 && m.PriceChange5m > 0 {
		score += 0.10 // trending up on both timeframes
	} else if m.PriceChange1h < -15 {
		score -= 0.15 // 1h trend is down
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	sig.MomentumScore = score

	// ── 4. Direction ────────────────────────────────────────────────────────
	if score >= 0.65 {
		sig.MomentumDirection = "up"
	} else if score <= 0.35 {
		sig.MomentumDirection = "down"
	} else {
		sig.MomentumDirection = "flat"
	}
}

// ── Market Regime Detector ───────────────────────────────────────────────────

// MarketRegimeDetector fetches SOL price trend to classify the current market regime.
type MarketRegimeDetector struct {
	cache       *regimeCache
}

type regimeCache struct {
	Regime    string
	SOL5m     float64
	SOL1h     float64
	FetchedAt time.Time
}

type dexResp struct {
	Pairs []struct {
		PriceChange struct {
			M5 float64 `json:"m5"`
			H1 float64 `json:"h1"`
		} `json:"priceChange"`
	} `json:"pairs"`
}

func NewMarketRegimeDetector() *MarketRegimeDetector {
	return &MarketRegimeDetector{}
}

// Analyze detects the market regime (bull/bear/sideways) from SOL price trend.
func (d *MarketRegimeDetector) Analyze(sig *PipelineSignal) {
	// Use cached regime if fresh (< 5 min old)
	if d.cache != nil && time.Since(d.cache.FetchedAt) < 5*time.Minute {
		sig.MarketRegime = d.cache.Regime
		sig.SolTrend5m = d.cache.SOL5m
		sig.SolTrend1h = d.cache.SOL1h
		return
	}

	// Fetch SOL price change from DexScreener
	sol5m, sol1h := fetchSOLTrend()

	regime := "sideways"
	if sol1h > 3 && sol5m > 0 {
		regime = "bull"
	} else if sol1h < -3 || sol5m < -5 {
		regime = "bear"
	}

	d.cache = &regimeCache{
		Regime:    regime,
		SOL5m:     sol5m,
		SOL1h:     sol1h,
		FetchedAt: time.Now(),
	}

	sig.MarketRegime = regime
	sig.SolTrend5m = sol5m
	sig.SolTrend1h = sol1h
}

func fetchSOLTrend() (float64, float64) {
	url := "https://api.dexscreener.com/latest/dex/tokens/So11111111111111111111111111111111111111112"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()

	var data dexResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || len(data.Pairs) == 0 {
		return 0, 0
	}
	return data.Pairs[0].PriceChange.M5, data.Pairs[0].PriceChange.H1
}

// ── Confidence Engine ────────────────────────────────────────────────────────

// ConfidenceEngine combines all engine signals into a final confidence score.
type ConfidenceEngine struct{}

func NewConfidenceEngine() *ConfidenceEngine {
	return &ConfidenceEngine{}
}

// Compute calculates a final confidence score (0–1) from all pipeline signals.
func (e *ConfidenceEngine) Compute(sig *PipelineSignal) {
	breakdown := make(map[string]float64)

	// ── 1. Organic & wash quality (20%) ─────────────────────────────────────
	organicFactor := (sig.Metrics.OrganicScore / 100.0) * 0.20
	washPenalty := sig.Metrics.WashTradeProbability * -0.10
	breakdown["organic"] = organicFactor + washPenalty

	// ── 2. Momentum (20%) ───────────────────────────────────────────────────
	momentumFactor := sig.MomentumScore * 0.20
	breakdown["momentum"] = momentumFactor

	// ── 3. Deployer reputation (15%) ────────────────────────────────────────
	deployerFactor := sig.DeployerReputationScore * 0.15
	breakdown["deployer"] = deployerFactor

	// ── 4. Holder distribution (15%) ────────────────────────────────────────
	holderFactor := sig.HolderDistributionScore * 0.15
	breakdown["holder"] = holderFactor

	// ── 5. Liquidity stability (10%) ────────────────────────────────────────
	liqFactor := 0.0
	if sig.LiquidityIsStable {
		liqFactor = 0.10
	} else if sig.LiquidityTrend == "growing" {
		liqFactor = 0.08
	} else if sig.LiquidityTrend == "rug" {
		liqFactor = -0.15
	}
	breakdown["liquidity"] = liqFactor

	// ── 6. Jupiter price impact (10%) ───────────────────────────────────────
	jupFactor := 0.0
	if sig.JupiterPriceImpactPct < 1.0 {
		jupFactor = 0.10 // low slippage = good liquidity
	} else if sig.JupiterPriceImpactPct < 3.0 {
		jupFactor = 0.06
	} else if sig.JupiterPriceImpactPct > 5.0 {
		jupFactor = -0.05 // high slippage = risky
	}
	breakdown["jupiter"] = jupFactor

	// ── 7. Market regime (10%) ──────────────────────────────────────────────
	regimeFactor := 0.0
	switch sig.MarketRegime {
	case "bull":
		regimeFactor = 0.10
	case "sideways":
		regimeFactor = 0.05
	case "bear":
		regimeFactor = -0.05
	}
	breakdown["regime"] = regimeFactor

	// ── Sum ─────────────────────────────────────────────────────────────────
	total := 0.0
	for _, v := range breakdown {
		total += v
	}
	if total < 0 {
		total = 0
	}
	if total > 1 {
		total = 1
	}

	sig.ConfidenceScore = total
	sig.ConfidenceBreakdown = breakdown
}

// ── Dynamic Position Sizing ──────────────────────────────────────────────────

// DynamicSizer computes the recommended position size based on confidence.
type DynamicSizer struct {
	maxSizeSOL float64
	minSizeSOL float64
}

func NewDynamicSizer(minSOL, maxSOL float64) *DynamicSizer {
	return &DynamicSizer{minSizeSOL: minSOL, maxSizeSOL: maxSOL}
}

// Size calculates the recommended position size (SOL) based on confidence score.
func (s *DynamicSizer) Size(sig *PipelineSignal) {
	c := sig.ConfidenceScore

	var sizeSOL float64
	var reason string

	switch {
	case c >= 0.85:
		sizeSOL = s.maxSizeSOL
		reason = fmt.Sprintf("FULL SIZE — very high confidence (%.0f%%)", c*100)
	case c >= 0.70:
		sizeSOL = s.maxSizeSOL * 0.75
		reason = fmt.Sprintf("75%% SIZE — high confidence (%.0f%%)", c*100)
	case c >= 0.60:
		sizeSOL = s.maxSizeSOL * 0.50
		reason = fmt.Sprintf("50%% SIZE — moderate confidence (%.0f%%)", c*100)
	case c >= 0.50:
		sizeSOL = s.maxSizeSOL * 0.25
		reason = fmt.Sprintf("25%% SIZE — low confidence (%.0f%%)", c*100)
	default:
		sizeSOL = 0
		reason = fmt.Sprintf("NO ENTRY — confidence too low (%.0f%%)", c*100)
	}

	// Bear market penalty
	if sig.MarketRegime == "bear" && sizeSOL > 0 {
		sizeSOL *= 0.5
		reason += " [bear penalty -50%]"
	}

	if sizeSOL < s.minSizeSOL && sizeSOL > 0 {
		sizeSOL = s.minSizeSOL
	}

	sig.RecommendedSizeSOL = sizeSOL
	sig.SizingReason = reason
}

// ── Portfolio Risk Engine ────────────────────────────────────────────────────

// PortfolioRiskEngine enforces portfolio-level risk limits.
type PortfolioRiskEngine struct{}

func NewPortfolioRiskEngine() *PortfolioRiskEngine {
	return &PortfolioRiskEngine{}
}

// Check validates that adding this position won't violate portfolio limits.
// Returns false (reject) with a reason if limits are exceeded.
func (e *PortfolioRiskEngine) Check(sig *PipelineSignal, openPositions int, totalCapitalAtRisk float64, walletBalanceSOL float64, maxPositions int, maxCapitalPct float64) bool {
	// Max open positions
	if openPositions >= maxPositions {
		sig.RejectedBy = fmt.Sprintf("PortfolioRisk: max positions reached (%d/%d)", openPositions, maxPositions)
		return false
	}

	// Max capital at risk (% of wallet)
	maxCapital := walletBalanceSOL * (maxCapitalPct / 100.0)
	if totalCapitalAtRisk+sig.RecommendedSizeSOL > maxCapital {
		sig.RejectedBy = fmt.Sprintf("PortfolioRisk: capital limit exceeded (%.2f+%.2f > %.2f SOL)",
			totalCapitalAtRisk, sig.RecommendedSizeSOL, maxCapital)
		return false
	}

	// No entry in bear market if already have positions
	if sig.MarketRegime == "bear" && openPositions > 0 {
		sig.RejectedBy = "PortfolioRisk: bear market — no new positions when existing positions open"
		return false
	}

	return true
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func solPrice(liqSOL, liqUSD float64) float64 {
	if liqSOL == 0 {
		return 150.0
	}
	return liqUSD / liqSOL
}
