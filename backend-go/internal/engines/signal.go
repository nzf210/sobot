package engines

import (
	"time"

	"hybrid-solana-bot/internal/models"
)

// PipelineSignal carries all data through the entire analysis pipeline.
// Each engine reads what it needs and writes its output back into this struct.
type PipelineSignal struct {
	// ── Input ──────────────────────────────────────────────────────────────
	Metrics  models.TokenMetrics
	Source   string    // "pumpfun", "raydium", "meteora", "dexscreener"
	SeenAt   time.Time

	// ── Deployer Reputation Engine ─────────────────────────────────────────
	DeployerAddress        string  `json:"deployer_address"`
	DeployerRugCount       int     `json:"deployer_rug_count"`
	DeployerTotalTokens    int     `json:"deployer_total_tokens"`
	DeployerReputationScore float64 `json:"deployer_reputation_score"` // 0–1 (1=safe)

	// ── Wallet Cluster Detection ───────────────────────────────────────────
	WalletClusterDetected  bool    `json:"wallet_cluster_detected"`
	ClusterWalletCount     int     `json:"cluster_wallet_count"`
	ClusterBuyPct          float64 `json:"cluster_buy_pct"` // % of volume from cluster

	// ── Holder Distribution Engine ─────────────────────────────────────────
	Top10HolderPct         float64 `json:"top10_holder_pct"`
	Top1HolderPct          float64 `json:"top1_holder_pct"`
	HolderCount            int     `json:"holder_count"`
	HolderDistributionScore float64 `json:"holder_distribution_score"` // 0–1 (1=well distributed)

	// ── Liquidity Stability Engine ─────────────────────────────────────────
	LiquidityChangeRate    float64 `json:"liquidity_change_rate"` // % change in last 5m
	LiquidityIsStable      bool    `json:"liquidity_is_stable"`
	LiquidityTrend         string  `json:"liquidity_trend"` // "growing", "stable", "shrinking", "rug"

	// ── Jupiter Intelligence ───────────────────────────────────────────────
	JupiterPriceImpactPct  float64 `json:"jupiter_price_impact_pct"`  // slippage for our order size
	JupiterLiquidityScore  float64 `json:"jupiter_liquidity_score"`    // 0–1
	JupiterBestRoute       string  `json:"jupiter_best_route"`

	// ── Momentum Engine ───────────────────────────────────────────────────
	MomentumScore          float64 `json:"momentum_score"` // 0–1
	MomentumDirection      string  `json:"momentum_direction"` // "up", "down", "flat"
	VolumeAcceleration     float64 `json:"volume_acceleration"` // vol5m/vol1h normalized
	PriceMomentumZ         float64 `json:"price_momentum_z"`    // Z-score of price change

	// ── Market Regime Detector ────────────────────────────────────────────
	MarketRegime           string  `json:"market_regime"` // "bull", "bear", "sideways"
	SolTrend5m             float64 `json:"sol_trend_5m"`
	SolTrend1h             float64 `json:"sol_trend_1h"`

	// ── Confidence Engine ─────────────────────────────────────────────────
	ConfidenceScore        float64 `json:"confidence_score"` // 0–1 final combined score
	ConfidenceBreakdown    map[string]float64 `json:"confidence_breakdown"`

	// ── Dynamic Position Sizing ───────────────────────────────────────────
	RecommendedSizeSOL     float64 `json:"recommended_size_sol"`
	SizingReason           string  `json:"sizing_reason"`

	// ── LLM Analysis ──────────────────────────────────────────────────────
	LLMDecision            string  `json:"llm_decision"`
	LLMConfidence          float64 `json:"llm_confidence"`
	LLMNarrativeScore      float64 `json:"llm_narrative_score"`
	LLMDLMMSuitability     float64 `json:"llm_dlmm_suitability"`

	// ── Pipeline flags ────────────────────────────────────────────────────
	RejectedBy             string  `json:"rejected_by,omitempty"` // which engine rejected it
	Approved               bool    `json:"approved"`
}
