package engines

import (
	"fmt"
)

// RuleEngine enforces trading rules and validates pipeline signals before execution.
type RuleEngine struct {
	config RuleEngineConfig
}

type RuleEngineConfig struct {
	MinConfidenceScore   float64
	MinLiquiditySOL      float64
	MaxLiquiditySOL      float64
	MinVolume5mSOL       float64
	MinOrganicScore      float64
	MaxWashTradePct      float64
	MinMarketCapSOL      float64
	MaxMarketCapSOL      float64
	MaxTop10HolderPct    float64
	MinHolderCount       int
	MaxDeployAmountSOL   float64
	AllowBearMarket      bool
	MaxOpenPositions     int
	MaxCapitalAtRiskSOL  float64
	DailyLossLimitUsd    float64
	MaxConsecutiveLosses int
	DryRun               bool
}

// NewRuleEngine creates a rule engine with the given configuration.
func NewRuleEngine(config RuleEngineConfig) *RuleEngine {
	return &RuleEngine{config: config}
}

// RuleViolation represents a failed rule check.
type RuleViolation struct {
	Rule    string
	Message string
}

// ValidateMetrics runs metric-only rule checks (no engine output needed).
// These check DexScreener data that is available immediately after fetch.
// Returns (isValid, violations)
func (e *RuleEngine) ValidateMetrics(sig *PipelineSignal) (bool, []RuleViolation) {
	var violations []RuleViolation

	// ── Liquidity gates ──────────────────────────────────────────────────────
	if sig.Metrics.LiquiditySOL < e.config.MinLiquiditySOL {
		violations = append(violations, RuleViolation{
			Rule:    "min_liquidity",
			Message: fmt.Sprintf("Liquidity %.2f SOL below minimum %.2f SOL", sig.Metrics.LiquiditySOL, e.config.MinLiquiditySOL),
		})
	}
	if e.config.MaxLiquiditySOL > 0 && sig.Metrics.LiquiditySOL > e.config.MaxLiquiditySOL {
		violations = append(violations, RuleViolation{
			Rule:    "max_liquidity",
			Message: fmt.Sprintf("Liquidity %.2f SOL exceeds maximum %.2f SOL", sig.Metrics.LiquiditySOL, e.config.MaxLiquiditySOL),
		})
	}

	// ── Volume gate ──────────────────────────────────────────────────────────
	if sig.Metrics.Volume5mSOL < e.config.MinVolume5mSOL {
		violations = append(violations, RuleViolation{
			Rule:    "min_volume",
			Message: fmt.Sprintf("5m volume %.2f SOL below minimum %.2f SOL", sig.Metrics.Volume5mSOL, e.config.MinVolume5mSOL),
		})
	}

	// ── Quality gates ────────────────────────────────────────────────────────
	if sig.Metrics.OrganicScore < e.config.MinOrganicScore {
		violations = append(violations, RuleViolation{
			Rule:    "min_organic",
			Message: fmt.Sprintf("Organic score %.0f below minimum %.0f", sig.Metrics.OrganicScore, e.config.MinOrganicScore),
		})
	}
	if sig.Metrics.WashTradeProbability > e.config.MaxWashTradePct/100.0 {
		violations = append(violations, RuleViolation{
			Rule:    "max_wash_trade",
			Message: fmt.Sprintf("Wash trade probability %.0f%% exceeds maximum %.0f%%", sig.Metrics.WashTradeProbability*100, e.config.MaxWashTradePct),
		})
	}

	// ── Market cap gates ─────────────────────────────────────────────────────
	if sig.Metrics.MarketCapSOL < e.config.MinMarketCapSOL {
		violations = append(violations, RuleViolation{
			Rule:    "min_mcap",
			Message: fmt.Sprintf("Market cap %.2f SOL below minimum %.2f SOL", sig.Metrics.MarketCapSOL, e.config.MinMarketCapSOL),
		})
	}
	if e.config.MaxMarketCapSOL > 0 && sig.Metrics.MarketCapSOL > e.config.MaxMarketCapSOL {
		violations = append(violations, RuleViolation{
			Rule:    "max_mcap",
			Message: fmt.Sprintf("Market cap %.2f SOL exceeds maximum %.2f SOL", sig.Metrics.MarketCapSOL, e.config.MaxMarketCapSOL),
		})
	}

	return len(violations) == 0, violations
}

// ValidateEngines runs engine-dependent rule checks.
// Must be called after all engines have populated the signal fields.
// Returns (isValid, violations)
func (e *RuleEngine) ValidateEngines(sig *PipelineSignal) (bool, []RuleViolation) {
	var violations []RuleViolation

	// ── Confidence gate ──────────────────────────────────────────────────────
	if sig.ConfidenceScore < e.config.MinConfidenceScore {
		violations = append(violations, RuleViolation{
			Rule:    "min_confidence",
			Message: fmt.Sprintf("Confidence %.2f below threshold %.2f", sig.ConfidenceScore, e.config.MinConfidenceScore),
		})
	}

	// ── Deployer reputation ──────────────────────────────────────────────────
	if sig.DeployerReputationScore < 0.4 {
		violations = append(violations, RuleViolation{
			Rule:    "deployer_reputation",
			Message: fmt.Sprintf("Deployer reputation %.2f too low", sig.DeployerReputationScore),
		})
	}
	if sig.DeployerRugCount > 3 {
		violations = append(violations, RuleViolation{
			Rule:    "deployer_rugs",
			Message: fmt.Sprintf("Deployer has %d rug history", sig.DeployerRugCount),
		})
	}

	// ── Holder concentration ─────────────────────────────────────────────────
	if sig.Top10HolderPct > e.config.MaxTop10HolderPct {
		violations = append(violations, RuleViolation{
			Rule:    "holder_concentration",
			Message: fmt.Sprintf("Top 10 holders %.0f%% exceeds maximum %.0f%%", sig.Top10HolderPct, e.config.MaxTop10HolderPct),
		})
	}
	if sig.HolderCount > 0 && sig.HolderCount < e.config.MinHolderCount {
		violations = append(violations, RuleViolation{
			Rule:    "min_holders",
			Message: fmt.Sprintf("Holder count %d below minimum %d", sig.HolderCount, e.config.MinHolderCount),
		})
	}

	// ── Liquidity stability ──────────────────────────────────────────────────
	if sig.LiquidityTrend == "rug" {
		violations = append(violations, RuleViolation{
			Rule:    "liquidity_rug",
			Message: "Liquidity rug pattern detected",
		})
	}
	if !sig.LiquidityIsStable && sig.LiquidityTrend == "shrinking" {
		violations = append(violations, RuleViolation{
			Rule:    "liquidity_shrinking",
			Message: "Liquidity is shrinking",
		})
	}

	// ── Wallet cluster ───────────────────────────────────────────────────────
	if sig.WalletClusterDetected && sig.ClusterBuyPct > 70 {
		violations = append(violations, RuleViolation{
			Rule:    "wallet_cluster",
			Message: fmt.Sprintf("Coordinated wallet cluster detected (%.0f%% buys)", sig.ClusterBuyPct),
		})
	}

	// ── Jupiter price impact ─────────────────────────────────────────────────
	if sig.JupiterPriceImpactPct > 5.0 {
		violations = append(violations, RuleViolation{
			Rule:    "high_slippage",
			Message: fmt.Sprintf("Price impact %.2f%% too high", sig.JupiterPriceImpactPct),
		})
	}

	// ── Momentum direction ───────────────────────────────────────────────────
	if sig.MomentumDirection == "down" && sig.MomentumScore < 0.3 {
		violations = append(violations, RuleViolation{
			Rule:    "negative_momentum",
			Message: "Strong downward momentum",
		})
	}

	// ── Market regime ────────────────────────────────────────────────────────
	if !e.config.AllowBearMarket && sig.MarketRegime == "bear" {
		violations = append(violations, RuleViolation{
			Rule:    "bear_market",
			Message: "Bear market — trading disabled",
		})
	}

	// ── Position size limit ──────────────────────────────────────────────────
	if sig.RecommendedSizeSOL > e.config.MaxDeployAmountSOL {
		violations = append(violations, RuleViolation{
			Rule:    "max_position_size",
			Message: fmt.Sprintf("Recommended size %.2f SOL exceeds maximum %.2f SOL", sig.RecommendedSizeSOL, e.config.MaxDeployAmountSOL),
		})
	}

	return len(violations) == 0, violations
}

// Validate runs all rule checks (metrics + engines) against the pipeline signal.
// Deprecated: use ValidateMetrics and ValidateEngines separately in the pipeline.
func (e *RuleEngine) Validate(sig *PipelineSignal) (bool, []RuleViolation) {
	valid1, v1 := e.ValidateMetrics(sig)
	valid2, v2 := e.ValidateEngines(sig)
	all := append(v1, v2...)
	return valid1 && valid2, all
}

// ValidatePortfolio checks portfolio-level constraints.
func (e *RuleEngine) ValidatePortfolio(sig *PipelineSignal, openPositions int, totalCapitalAtRiskSOL float64, walletBalanceSOL float64, dailyLossUsd float64, consecutiveLosses int) (bool, []RuleViolation) {
	var violations []RuleViolation

	// ── Max open positions ───────────────────────────────────────────────────
	if openPositions >= e.config.MaxOpenPositions {
		violations = append(violations, RuleViolation{
			Rule:    "max_positions",
			Message: fmt.Sprintf("Max open positions reached (%d/%d)", openPositions, e.config.MaxOpenPositions),
		})
	}

	// ── Capital at risk ──────────────────────────────────────────────────────
	if totalCapitalAtRiskSOL+sig.RecommendedSizeSOL > e.config.MaxCapitalAtRiskSOL {
		violations = append(violations, RuleViolation{
			Rule:    "max_capital",
			Message: fmt.Sprintf("Total capital at risk (%.2f + %.2f = %.2f SOL) exceeds limit (%.2f SOL)",
				totalCapitalAtRiskSOL, sig.RecommendedSizeSOL, totalCapitalAtRiskSOL+sig.RecommendedSizeSOL, e.config.MaxCapitalAtRiskSOL),
		})
	}

	// ── Bear market with existing positions ──────────────────────────────────
	if sig.MarketRegime == "bear" && openPositions > 0 {
		violations = append(violations, RuleViolation{
			Rule:    "bear_existing_positions",
			Message: "Bear market with existing open positions — no new entries",
		})
	}

	// ── Daily loss limit ─────────────────────────────────────────────────────
	if e.config.DailyLossLimitUsd > 0 && dailyLossUsd >= e.config.DailyLossLimitUsd {
		violations = append(violations, RuleViolation{
			Rule:    "daily_loss_limit",
			Message: fmt.Sprintf("Daily loss limit reached ($%.2f / $%.2f)", dailyLossUsd, e.config.DailyLossLimitUsd),
		})
	}

	// ── Max consecutive losses ───────────────────────────────────────────────
	if e.config.MaxConsecutiveLosses > 0 && consecutiveLosses >= e.config.MaxConsecutiveLosses {
		violations = append(violations, RuleViolation{
			Rule:    "max_consecutive_losses",
			Message: fmt.Sprintf("Max consecutive losses reached (%d/%d)", consecutiveLosses, e.config.MaxConsecutiveLosses),
		})
	}

	return len(violations) == 0, violations
}
