package engine

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"btc-treasury/internal/llm"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/models"
)

const (
	cacheTTLSecs      = 300 // 5 minutes
	pairCooldownSecs  = 300 // 5 minutes per pair
)

type cacheEntry struct {
	advisory models.FullBtcAdvisory
	cachedAt time.Time
}

type AdvisoryEngine struct {
	llm         *llm.LlmClient
	mem         *memory.MemoryStore
	cache       map[string]cacheEntry
	cacheLock   sync.RWMutex
	lastLlmCall map[string]time.Time
	cooldownLock sync.RWMutex
}

func NewAdvisoryEngine(llmURL, llmModel, llmAPIKey string, mem *memory.MemoryStore) *AdvisoryEngine {
	return &AdvisoryEngine{
		llm:         llm.NewLlmClient(llmURL, llmModel, llmAPIKey),
		mem:         mem,
		cache:       make(map[string]cacheEntry),
		lastLlmCall: make(map[string]time.Time),
	}
}

func (ae *AdvisoryEngine) cacheKey(pair, regime string, score float64) string {
	// Bucket score into 5-point buckets to maximize cache hits on nearly-identical scans.
	scoreBucket := int(math.Floor(score / 5.0))
	return fmt.Sprintf("%s|%s|%d", pair, regime, scoreBucket)
}

func (ae *AdvisoryEngine) cacheGet(key string) (models.FullBtcAdvisory, bool) {
	ae.cacheLock.RLock()
	defer ae.cacheLock.RUnlock()
	entry, ok := ae.cache[key]
	if ok && time.Since(entry.cachedAt) < cacheTTLSecs*time.Second {
		return entry.advisory, true
	}
	return models.FullBtcAdvisory{}, false
}

func (ae *AdvisoryEngine) cachePut(key string, advisory models.FullBtcAdvisory) {
	ae.cacheLock.Lock()
	defer ae.cacheLock.Unlock()
	ae.cache[key] = cacheEntry{advisory: advisory, cachedAt: time.Now()}

	// Opportunistic GC
	if len(ae.cache) > 256 {
		cutoff := time.Now().Add(-cacheTTLSecs * time.Second)
		for k, v := range ae.cache {
			if v.cachedAt.Before(cutoff) {
				delete(ae.cache, k)
			}
		}
	}
}

func (ae *AdvisoryEngine) cooldownElapsed(pair string) bool {
	ae.cooldownLock.RLock()
	defer ae.cooldownLock.RUnlock()
	last, ok := ae.lastLlmCall[pair]
	if ok {
		return time.Since(last) >= pairCooldownSecs*time.Second
	}
	return true
}

func (ae *AdvisoryEngine) markLlmCalled(pair string) {
	ae.cooldownLock.Lock()
	defer ae.cooldownLock.Unlock()
	ae.lastLlmCall[pair] = time.Now()

	// GC
	if len(ae.lastLlmCall) > 64 {
		cutoff := time.Now().Add(-pairCooldownSecs * 2 * time.Second)
		for k, v := range ae.lastLlmCall {
			if v.Before(cutoff) {
				delete(ae.lastLlmCall, k)
			}
		}
	}
}

const systemPrompt = `BTC Treasury Accumulation AI. Goal: maximize Δ BTC (not USD).

CONSTRAINTS:
- SPOT only (no leverage/futures/perpetual). Universe: BTC-quote pairs.
- Max 1% risk/trade after fees. TP > |SL|. Max 1 active position.
- Score >= 80 → AMBIL. Score < 80 → DO NOTHING.
- 3 losses → 24h pause. Drawdown > 10% → reduce size 50%.
- Round-trip fee 0.2% (taker 0.1% × 2). SL must absorb fee.

DYNAMIC TP/SL BY REGIME (system auto-clamps SL to 1.5× ATR_14):
- CALM/RANGING: TP 2.5-4%, SL -0.8 to -1.2%
- TRENDING: TP 4-7%, SL -1.2 to -1.8%
- VOLATILE: TP 6-10%, SL -1.8 to -2.5%
- For positions ≤0.001 BTC: use upper TP, min SL (wider ratio).

ENTRY (all required): RS rising, EMA20>EMA50>EMA200, MACD bullish, vol>avg.
EXIT: TP, trailing stop (active), hard SL.

TREASURY: 50% compound + 50% BTC vault. Vault untouchable.

SCORING: 40% RS, 25% Volume, 20% Trend, 10% Vol quality, 5% Structure.

RECS: REJECT, MONITOR, APPROVE, REDUCE_EXPOSURE, EXIT_POSITION, PROTECT_TREASURY, ENABLE_SAFE_MODE.

PROHIBITED: predicting prices, guaranteeing profits, martingale, all-in, leverage, futures, USD-denominated success.

OUTPUT ONLY valid JSON. No markdown. No text outside JSON.

{
  "market_regime": "TRENDING_BULLISH",
  "opportunity_score": 82,
  "confidence": 0.84,
  "risk_level": "MEDIUM",
  "recommendation": "APPROVE",
  "treasury_mode": "ACCUMULATE",
  "reason": "Strong RS + Volume spike + EMA alignment.",
  "warnings": [],
  "dynamic_take_profit": 5.5,
  "dynamic_stop_loss": -1.2,
  "tp_reason": "TRENDING - 5.5% TP captures momentum above 4h resistance",
  "sl_reason": "1.2% SL + 0.2% fee = 1.4% max loss, below 1.5% support"
}`

func (ae *AdvisoryEngine) Analyze(ctx context.Context, input *models.BtcAdvisoryInput) models.FullBtcAdvisory {
	cfg := ae.mem.GetConfig()
	marketRegime := ClassifyRegime(&input.MarketData)

	riskLevel := "LOW"
	var warnings []string
	if input.RiskAssessment != nil {
		riskLevel = input.RiskAssessment.RiskLevel
	} else {
		riskLevel, warnings = AssessRisk(&input.MarketData, &input.Treasury, input.LossStreak)
	}

	treasuryMode := TreasuryMode(&input.MarketData, &input.Treasury, riskLevel)

	opportunity := OpportunityScore(&input.MarketData)
	if input.AiScore != nil {
		ai100 := math.Max(0.0, math.Min(100.0, *input.AiScore*10.0))
		opportunity = math.Round(ai100*0.6 + opportunity*0.4)
	}

	// ── EARLY-EXIT CHEAP GUARDS ──────────────────────────────────────
	if quantDecision := QuantFastPath(&input.MarketData, &input.Treasury, opportunity, riskLevel, marketRegime, input.LossStreak); quantDecision != nil {
		return *quantDecision
	}

	// ── LLM CACHE LOOKUP ──────────────────────────────────────────────
	cacheKey := ae.cacheKey(input.MarketData.Pair, marketRegime, opportunity)
	if cached, ok := ae.cacheGet(cacheKey); ok {
		return cached
	}

	// ── SHOULD WE ACTUALLY CALL LLM? ─────────────────────────────────
	if !ShouldActivateLLM(opportunity, &input.MarketData, riskLevel, marketRegime, &cfg) {
		return QuantAdvisory(&input.MarketData, marketRegime, riskLevel, warnings, opportunity, treasuryMode, cfg.TakerFeePct)
	}

	// ── COOLDOWN GATE ────────────────────────────────────────────────
	if !ae.cooldownElapsed(input.MarketData.Pair) {
		log.Printf("BTC [%s]: cooldown active, returning quant fallback", input.MarketData.Pair)
		return QuantAdvisory(&input.MarketData, marketRegime, riskLevel, warnings, opportunity, treasuryMode, cfg.TakerFeePct)
	}

	if !cfg.Enabled {
		return QuantAdvisory(&input.MarketData, marketRegime, riskLevel, warnings, opportunity, treasuryMode, cfg.TakerFeePct)
	}

	// ── ACTUAL LLM CALL ──────────────────────────────────────────────
	log.Printf("BTC Advisory [%s]: activating LLM", input.MarketData.Pair)

	advisory, err := ae.callLLM(ctx, input, marketRegime, riskLevel, warnings, opportunity, treasuryMode, &cfg)
	if err == nil {
		advisory.OpportunityScore = opportunity
		advisory.MarketRegime = marketRegime
		advisory.BypassQuant = true
		ae.cachePut(cacheKey, advisory)
		ae.markLlmCalled(input.MarketData.Pair)
		return advisory
	}

	log.Printf("BTC LLM call failed: %v", err)
	return QuantAdvisory(&input.MarketData, marketRegime, riskLevel, warnings, opportunity, treasuryMode, cfg.TakerFeePct)
}

func (ae *AdvisoryEngine) callLLM(
	ctx context.Context,
	input *models.BtcAdvisoryInput,
	regime, riskLevel string,
	warnings []string,
	opportunity float64,
	treasuryMode string,
	cfg *models.BtcConfig,
) (models.FullBtcAdvisory, error) {
	warningsStr := "none"
	if len(warnings) > 0 {
		warningsStr = strings.Join(warnings, "; ")
	}

	positionsStr := "none"
	if len(input.OpenPositions) > 0 {
		var parts []string
		for _, p := range input.OpenPositions {
			parts = append(parts, fmt.Sprintf("%s(%.2f%%)", p.ID, p.PnlBtc))
		}
		positionsStr = strings.Join(parts, ", ")
	}

	metricsSummary := "n/a"
	if input.PairMetrics != nil {
		pm := input.PairMetrics
		emaBull := "bear"
		if pm.EmaBullishAlignment {
			emaBull = "bull"
		}
		macdBull := "bear"
		if pm.MacdBullish {
			macdBull = "bull"
		}
		volSpike := "false"
		if pm.VolumeSpike {
			volSpike = "true"
		}
		metricsSummary = fmt.Sprintf(
			"RS=%.2f EMA=%s MACD=%s VolSpike=%s ATR%%=%.2f Spread=%.2f BidDepth=%.0f AskDepth=%.0f",
			pm.RsScore, emaBull, macdBull, volSpike, pm.Atr14, pm.SpreadPct, pm.BidDepth, pm.AskDepth,
		)
	}

	aiScoreStr := "n/a"
	if input.AiScore != nil {
		aiScoreStr = fmt.Sprintf("%.0f", *input.AiScore)
	}

	userPrompt := fmt.Sprintf(
		"Pair=%s Regime=%s Risk=%s Mode=%s Score=%.0f Conf=%.2f "+
			"Trend=%.1f Vol=%.1f Liq=%.1f Spread=%.1f Volatility=%.1f "+
			"BreakoutProb=%.2f ReversalProb=%.2f Exposure=%.2f DD=%.4f "+
			"FeeRT=%.2f%% LossStreak=%d AI=%s "+
			"Indicators: %s Positions: %s Warnings: %s "+
			"BTC=%.8f PrevBTC=%.8f Growth7d=%.4f "+
			"Strategy=%s",
		input.MarketData.Pair,
		regime,
		riskLevel,
		treasuryMode,
		opportunity,
		input.MarketData.Confidence,
		input.MarketData.TrendStrength,
		input.MarketData.VolumeScore,
		input.MarketData.LiquidityScore,
		input.MarketData.SpreadScore,
		input.MarketData.VolatilityScore,
		input.MarketData.BreakoutProbability,
		input.MarketData.ReversalProbability,
		input.MarketData.PortfolioExposure,
		input.MarketData.DailyDrawdown,
		cfg.TakerFeePct*200.0,
		input.LossStreak,
		aiScoreStr,
		metricsSummary,
		positionsStr,
		warningsStr,
		input.Treasury.CurrentBtc,
		input.Treasury.PreviousBtc,
		input.Treasury.BtcGrowth7d,
		input.MarketData.ActiveStrategy,
	)

	lessonsCtx := ae.mem.LoadLessonsContext()
	return ae.llm.Call(ctx, systemPrompt, userPrompt+lessonsCtx)
}
