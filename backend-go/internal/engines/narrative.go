package engines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/memory"
)

// LLMNarrativeAnalysis sends full pipeline context to the LLM and parses the response.
type LLMNarrativeAnalysis struct {
	url     string
	model   string
	apiKey  string
	log     *zap.Logger
	mem     *memory.MemoryStore
}

func NewLLMNarrativeAnalysis(url, model, apiKey string, mem *memory.MemoryStore, log *zap.Logger) *LLMNarrativeAnalysis {
	return &LLMNarrativeAnalysis{
		url:     url,
		model:   model,
		apiKey:  apiKey,
		log:     log,
		mem:     mem,
	}
}

// LLMResult contains the parsed LLM output.
type LLMResult struct {
	Decision        string  `json:"decision"`
	Confidence      float64 `json:"confidence"`
	NarrativeScore  float64 `json:"narrative_score"`
	DLMMSuitability float64 `json:"dlmm_suitability"`
	Reasoning       string  `json:"reasoning"`
}

// Analyze sends enriched pipeline signal to LLM and returns result.
func (a *LLMNarrativeAnalysis) Analyze(sig *PipelineSignal) {
	ctxStr := a.mem.LoadContext()

	prompt := a.buildPrompt(sig, ctxStr)
	result, err := a.callLLM(prompt)
	if err != nil {
		a.log.Error("LLM analysis failed", zap.Error(err))
		// Fallback
		result = LLMResult{
			Decision:        "HOLD",
			Confidence:      0.5,
			NarrativeScore:  0.5,
			DLMMSuitability: 0.3,
			Reasoning:       "LLM unavailable, using heuristic fallback",
		}
	}

	sig.LLMDecision = result.Decision
	sig.LLMConfidence = result.Confidence
	sig.LLMNarrativeScore = result.NarrativeScore
	sig.LLMDLMMSuitability = result.DLMMSuitability
}

func (a *LLMNarrativeAnalysis) buildPrompt(sig *PipelineSignal, ctx string) string {
	m := sig.Metrics

	return fmt.Sprintf(`You are a crypto trading AI analyzing Solana tokens. Output ONLY a JSON object:
{
  "decision": "string (BUY, SELL, HOLD, MICRO_ENTRY_ONLY)",
  "confidence": "number between 0 and 1",
  "narrative_score": "number between 0 and 1",
  "dlmm_suitability": "number between 0 and 1",
  "reasoning": "string (brief explanation)"
}

Bot Memory Context (learn from past lessons):
%s

Pipeline Analysis Results:
- Token: %s
- Source: %s
- Liquidity: $%.0f (%.2f SOL)
- Market Cap: $%.0f (%.2f SOL)
- Volume 5m: $%.0f (%.2f SOL)
- Volume 1h: $%.0f
- Buy/Sell Ratio: %.2f (Buys: %d, Sells: %d in 5m)
- Organic Score: %.0f/100
- Wash Trade Probability: %.0f%%

Engine Results:
- Deployer Reputation: %.2f (Rugs: %d, Tokens: %d)
- Holder Distribution: %.2f (Top10: %.0f%%, Holders: %d)
- Liquidity Trend: %s (Stable: %v, Change: %.1f%%)
- Wallet Cluster Detected: %v (Buy Pct: %.0f%%)
- Jupiter Price Impact: %.2f%% (Liquidity Score: %.2f)
- Momentum Score: %.2f (Direction: %s, Vol Accel: %.2f, Z-Score: %.2f)
- Market Regime: %s (SOL 5m: %.2f%%, 1h: %.2f%%)
- Confidence Score: %.2f

Given all this, should we BUY, SELL, HOLD, or take MICRO_ENTRY_ONLY?`,
		ctx,
		m.Token, sig.Source,
		m.LiquidityUSD, m.LiquiditySOL,
		m.MarketCap, m.MarketCapSOL,
		m.Volume5m, m.Volume5mSOL,
		m.Volume1h,
		m.BuySellRatio, m.Buys5m, m.Sells5m,
		m.OrganicScore, m.WashTradeProbability*100,
		sig.DeployerReputationScore, sig.DeployerRugCount, sig.DeployerTotalTokens,
		sig.HolderDistributionScore, sig.Top10HolderPct, sig.HolderCount,
		sig.LiquidityTrend, sig.LiquidityIsStable, sig.LiquidityChangeRate,
		sig.WalletClusterDetected, sig.ClusterBuyPct,
		sig.JupiterPriceImpactPct, sig.JupiterLiquidityScore,
		sig.MomentumScore, sig.MomentumDirection, sig.VolumeAcceleration, sig.PriceMomentumZ,
		sig.MarketRegime, sig.SolTrend5m, sig.SolTrend1h,
		sig.ConfidenceScore,
	)
}

func (a *LLMNarrativeAnalysis) callLLM(prompt string) (LLMResult, error) {
	reqBody := map[string]interface{}{
		"model": a.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a crypto trading AI. Output strictly valid JSON."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.373,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return LLMResult{}, err
	}

	url := fmt.Sprintf("%s/chat/completions", a.url)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return LLMResult{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.apiKey))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return LLMResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return LLMResult{}, fmt.Errorf("LLM API returned status: %s, body: %s", resp.Status, string(bodyBytes))
	}

	var aiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		return LLMResult{}, err
	}

	if len(aiResp.Choices) == 0 {
		return LLMResult{}, fmt.Errorf("no response from LLM")
	}

	var result LLMResult
	if err := json.Unmarshal([]byte(aiResp.Choices[0].Message.Content), &result); err != nil {
		return LLMResult{}, fmt.Errorf("failed to parse LLM JSON: %v", err)
	}

	return result, nil
}
