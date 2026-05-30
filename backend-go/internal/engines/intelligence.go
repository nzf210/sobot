package engines

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DeployerReputationEngine checks a deployer wallet's history for rugs and past tokens.
// Uses Solscan public API (free tier).
type DeployerReputationEngine struct {
	cache map[string]*deployerResult
}

type deployerResult struct {
	Score     float64
	RugCount  int
	TotalTokens int
	CachedAt  time.Time
}

func NewDeployerReputationEngine() *DeployerReputationEngine {
	return &DeployerReputationEngine{cache: make(map[string]*deployerResult)}
}

// Analyze fetches deployer reputation and scores it.
func (e *DeployerReputationEngine) Analyze(sig *PipelineSignal) {
	addr := sig.DeployerAddress
	if addr == "" {
		// No deployer info — neutral score
		sig.DeployerReputationScore = 0.6
		return
	}

	// Use cache (TTL 30 min)
	if r, ok := e.cache[addr]; ok && time.Since(r.CachedAt) < 30*time.Minute {
		sig.DeployerReputationScore = r.Score
		sig.DeployerRugCount = r.RugCount
		sig.DeployerTotalTokens = r.TotalTokens
		return
	}

	score, rugCount, totalTokens := e.fetchDeployerScore(addr)

	e.cache[addr] = &deployerResult{
		Score:       score,
		RugCount:    rugCount,
		TotalTokens: totalTokens,
		CachedAt:    time.Now(),
	}

	sig.DeployerReputationScore = score
	sig.DeployerRugCount = rugCount
	sig.DeployerTotalTokens = totalTokens
}

// fetchDeployerScore queries Solscan for deployer token history.
// Returns: reputation score (0–1), rug count, total tokens deployed.
func (e *DeployerReputationEngine) fetchDeployerScore(walletAddr string) (float64, int, int) {
	// Use DexScreener to search tokens by deployer address
	url := fmt.Sprintf("https://api.dexscreener.com/latest/dex/search?q=%s", walletAddr)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0.6, 0, 0 // neutral if API fails
	}
	defer resp.Body.Close()

	var data struct {
		Pairs []struct {
			Liquidity struct{ Usd float64 `json:"usd"` } `json:"liquidity"`
			Volume    struct{ H24 float64 `json:"h24"` } `json:"volume"`
		} `json:"pairs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0.6, 0, 0
	}

	total := len(data.Pairs)
	if total == 0 {
		// New deployer — slightly lower trust (unknown)
		return 0.5, 0, 0
	}

	// Heuristic rug detection: pairs with near-zero liquidity (<$100) after creation
	rugCount := 0
	for _, p := range data.Pairs {
		if p.Liquidity.Usd < 100 && p.Volume.H24 < 10 {
			rugCount++
		}
	}

	rugRate := float64(rugCount) / float64(total)

	// Score: starts at 0.8, decreases with rug rate
	score := 0.80 - (rugRate * 0.70)
	if score < 0.05 {
		score = 0.05
	}
	if score > 1.0 {
		score = 1.0
	}

	return score, rugCount, total
}

// ── Holder Distribution Engine ───────────────────────────────────────────────

// HolderDistributionEngine checks top holder concentration.
// Uses DexScreener pair info for concentration proxy.
type HolderDistributionEngine struct {
	cache map[string]*holderResult
}

type holderResult struct {
	Score      float64
	Top10Pct   float64
	Top1Pct    float64
	HolderCount int
	CachedAt   time.Time
}

func NewHolderDistributionEngine() *HolderDistributionEngine {
	return &HolderDistributionEngine{cache: make(map[string]*holderResult)}
}

// Analyze estimates holder distribution from available metrics.
func (e *HolderDistributionEngine) Analyze(sig *PipelineSignal) {
	token := sig.Metrics.Token
	if token == "" {
		sig.HolderDistributionScore = 0.5
		return
	}

	if r, ok := e.cache[token]; ok && time.Since(r.CachedAt) < 10*time.Minute {
		sig.Top10HolderPct = r.Top10Pct
		sig.Top1HolderPct = r.Top1Pct
		sig.HolderCount = r.HolderCount
		sig.HolderDistributionScore = r.Score
		return
	}

	top10, top1, holderCount, score := e.estimateDistribution(sig)

	e.cache[token] = &holderResult{
		Score:       score,
		Top10Pct:    top10,
		Top1Pct:     top1,
		HolderCount: holderCount,
		CachedAt:    time.Now(),
	}

	sig.Top10HolderPct = top10
	sig.Top1HolderPct = top1
	sig.HolderCount = holderCount
	sig.HolderDistributionScore = score
}

func (e *HolderDistributionEngine) estimateDistribution(sig *PipelineSignal) (float64, float64, int, float64) {
	m := sig.Metrics

	// Use Solscan token holders API
	url := fmt.Sprintf("https://api.solscan.io/v2/token/holders?address=%s&page=1&page_size=10", m.Token)
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)

	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		var data struct {
			Data struct {
				Total   int `json:"total"`
				Holders []struct {
					Amount    float64 `json:"amount"`
					Decimals  int     `json:"decimals"`
				} `json:"holders"`
			} `json:"data"`
		}
		if json.NewDecoder(resp.Body).Decode(&data) == nil && len(data.Data.Holders) > 0 {
			total := data.Data.Total
			// Calculate top10 as approximate pct using BSR heuristic
			top10 := float64(len(data.Data.Holders)) / float64(total) * 100.0
			score := scoreHolderConc(top10, total)
			return top10, 0, total, score
		}
	}

	// Heuristic fallback: use buy/sell count and volume as proxy
	// More txns → more distributed
	txns := float64(m.Buys1h + m.Sells1h)
	estimatedHolders := int(txns * 0.7) // rough proxy
	if estimatedHolders < 10 {
		estimatedHolders = 10
	}

	// Use market cap vs liquidity ratio as distribution proxy
	top10 := 80.0 // default pessimistic
	if m.MarketCapSOL > 0 && m.LiquiditySOL > 0 {
		ratio := m.LiquiditySOL / m.MarketCapSOL
		if ratio > 0.1 { // >10% of mcap in LP = more distributed
			top10 = 50.0
		} else if ratio > 0.05 {
			top10 = 65.0
		}
	}

	score := scoreHolderConc(top10, estimatedHolders)
	return top10, 0, estimatedHolders, score
}

func scoreHolderConc(top10Pct float64, holderCount int) float64 {
	score := 1.0

	// Penalize concentration
	if top10Pct > 80 {
		score -= 0.50
	} else if top10Pct > 60 {
		score -= 0.30
	} else if top10Pct > 40 {
		score -= 0.10
	}

	// Reward larger holder counts
	if holderCount > 500 {
		score += 0.05
	} else if holderCount < 50 {
		score -= 0.10
	}

	if score < 0 {
		return 0
	}
	return score
}

// ── Liquidity Stability Engine ───────────────────────────────────────────────

// LiquidityStabilityEngine tracks liquidity changes to detect rugs.
type LiquidityStabilityEngine struct {
	history map[string][]liqSnapshot
}

type liqSnapshot struct {
	LiquiditySOL float64
	At           time.Time
}

func NewLiquidityStabilityEngine() *LiquidityStabilityEngine {
	return &LiquidityStabilityEngine{history: make(map[string][]liqSnapshot)}
}

// Analyze checks liquidity trend and stability.
func (e *LiquidityStabilityEngine) Analyze(sig *PipelineSignal) {
	token := sig.Metrics.Token
	currentLiq := sig.Metrics.LiquiditySOL

	// Record current snapshot
	snaps := e.history[token]
	snaps = append(snaps, liqSnapshot{LiquiditySOL: currentLiq, At: time.Now()})

	// Keep only last 10 snapshots (max ~50 min of history)
	if len(snaps) > 10 {
		snaps = snaps[len(snaps)-10:]
	}
	e.history[token] = snaps

	if len(snaps) < 2 {
		// Not enough history — assume stable
		sig.LiquidityIsStable = true
		sig.LiquidityTrend = "stable"
		sig.LiquidityChangeRate = 0
		return
	}

	// Compare most recent vs oldest in window
	oldest := snaps[0].LiquiditySOL
	changeRate := 0.0
	if oldest > 0 {
		changeRate = ((currentLiq - oldest) / oldest) * 100.0
	}
	sig.LiquidityChangeRate = changeRate

	// Check for sharp drops (rug pattern: >30% drop in window)
	if changeRate < -30 {
		sig.LiquidityTrend = "rug"
		sig.LiquidityIsStable = false
	} else if changeRate < -10 {
		sig.LiquidityTrend = "shrinking"
		sig.LiquidityIsStable = false
	} else if changeRate > 20 {
		sig.LiquidityTrend = "growing"
		sig.LiquidityIsStable = true
	} else {
		sig.LiquidityTrend = "stable"
		sig.LiquidityIsStable = true
	}
}

// ── Wallet Cluster Detection ─────────────────────────────────────────────────

// WalletClusterDetector detects coordinated wallet activity (shill/wash groups).
type WalletClusterDetector struct{}

func NewWalletClusterDetector() *WalletClusterDetector {
	return &WalletClusterDetector{}
}

// Analyze uses heuristics from available DexScreener metrics to detect clusters.
func (d *WalletClusterDetector) Analyze(sig *PipelineSignal) {
	m := sig.Metrics

	// Heuristic 1: extremely high BSR with few transactions = bot cluster
	// Real bots typically use many wallets but create a BSR skew
	clusterDetected := false
	clusterPct := 0.0
	clusterCount := 0

	if m.BuySellRatio > 6 && (m.Buys5m+m.Sells5m) < 20 {
		// Very skewed with few txns = likely coordinated
		clusterDetected = true
		clusterCount = m.Buys5m // assume all buys are from cluster
		clusterPct = float64(m.Buys5m) / float64(m.Buys5m+m.Sells5m+1) * 100
	}

	// Heuristic 2: Sudden volume spike with price barely moving = wash trading cluster
	if m.Volume5m > 50000 && absF(m.PriceChange5m) < 2 {
		clusterDetected = true
		clusterCount = 5 // unknown, estimate
		clusterPct = 60.0
	}

	// Cross-reference with wash trade probability
	if m.WashTradeProbability > 0.6 {
		clusterDetected = true
		clusterPct = m.WashTradeProbability * 80
	}

	sig.WalletClusterDetected = clusterDetected
	sig.ClusterWalletCount = clusterCount
	sig.ClusterBuyPct = clusterPct
}

// ── Jupiter Intelligence ─────────────────────────────────────────────────────

// JupiterIntelligence fetches price impact for our order size via Jupiter API.
type JupiterIntelligence struct {
	cache map[string]*jupResult
}

type jupResult struct {
	PriceImpact  float64
	LiqScore     float64
	BestRoute    string
	CachedAt     time.Time
}

func NewJupiterIntelligence() *JupiterIntelligence {
	return &JupiterIntelligence{cache: make(map[string]*jupResult)}
}

// Analyze fetches price impact data from Jupiter for the given token.
func (j *JupiterIntelligence) Analyze(sig *PipelineSignal, orderSizeSOL float64) {
	token := sig.Metrics.Token
	if token == "" {
		sig.JupiterPriceImpactPct = 5.0 // pessimistic default
		sig.JupiterLiquidityScore = 0.5
		return
	}

	if r, ok := j.cache[token]; ok && time.Since(r.CachedAt) < 2*time.Minute {
		sig.JupiterPriceImpactPct = r.PriceImpact
		sig.JupiterLiquidityScore = r.LiqScore
		sig.JupiterBestRoute = r.BestRoute
		return
	}

	impact, score, route := j.fetchPriceImpact(token, orderSizeSOL)

	j.cache[token] = &jupResult{
		PriceImpact: impact,
		LiqScore:    score,
		BestRoute:   route,
		CachedAt:    time.Now(),
	}

	sig.JupiterPriceImpactPct = impact
	sig.JupiterLiquidityScore = score
	sig.JupiterBestRoute = route
}

func (j *JupiterIntelligence) fetchPriceImpact(tokenAddr string, sizeSOL float64) (float64, float64, string) {
	// SOL → token, convert SOL amount to lamports
	lamports := int64(sizeSOL * 1e9)
	if lamports < 1000000 { // minimum 0.001 SOL
		lamports = 1000000
	}

	solMint := "So11111111111111111111111111111111111111112"
	url := fmt.Sprintf(
		"https://quote-api.jup.ag/v6/quote?inputMint=%s&outputMint=%s&amount=%d&slippageBps=300",
		solMint, tokenAddr, lamports,
	)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != 200 {
		// Use DexScreener liquidity as proxy
		return estimateImpactFromLiquidity(sizeSOL, 0), 0.5, "N/A"
	}
	defer resp.Body.Close()

	var data struct {
		PriceImpactPct float64 `json:"priceImpactPct"`
		RoutePlan []struct {
			SwapInfo struct {
				Label string `json:"label"`
			} `json:"swapInfo"`
		} `json:"routePlan"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 3.0, 0.5, "N/A"
	}

	impact := data.PriceImpactPct * 100 // convert from ratio to pct

	// Build route string
	routes := []string{}
	for _, r := range data.RoutePlan {
		if r.SwapInfo.Label != "" {
			routes = append(routes, r.SwapInfo.Label)
		}
	}
	route := strings.Join(routes, " → ")
	if route == "" {
		route = "Direct"
	}

	// Score based on impact
	score := 1.0
	if impact > 5 {
		score = 0.3
	} else if impact > 2 {
		score = 0.6
	} else if impact > 1 {
		score = 0.8
	}

	return impact, score, route
}

func estimateImpactFromLiquidity(sizeSOL, liqSOL float64) float64 {
	if liqSOL <= 0 {
		return 5.0
	}
	// Naive price impact estimate: (order / liquidity) * 100
	impact := (sizeSOL / liqSOL) * 100
	if impact > 20 {
		impact = 20
	}
	return impact
}
