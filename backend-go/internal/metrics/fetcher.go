package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hybrid-solana-bot/internal/models"
)

type DexScreenerResponse struct {
	Pairs []struct {
		Liquidity struct {
			Usd float64 `json:"usd"`
		} `json:"liquidity"`
		Fdv    float64 `json:"fdv"`
		Volume struct {
			M5 float64 `json:"m5"`
		} `json:"volume"`
		Txns struct {
			H24 struct {
				Buys  int `json:"buys"`
				Sells int `json:"sells"`
			} `json:"h24"`
		} `json:"txns"`
		PriceUsd string `json:"priceUsd"`
	} `json:"pairs"`
}

func FetchTokenMetrics(tokenAddress string) (models.TokenMetrics, error) {
	url := fmt.Sprintf("https://api.dexscreener.com/latest/dex/tokens/%s", tokenAddress)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return models.TokenMetrics{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.TokenMetrics{}, fmt.Errorf("dexscreener returned status %d", resp.StatusCode)
	}

	var data DexScreenerResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return models.TokenMetrics{}, err
	}

	if len(data.Pairs) == 0 {
		return models.TokenMetrics{}, fmt.Errorf("no pairs found for token %s", tokenAddress)
	}

	bestPair := data.Pairs[0]

	buySellRatio := 1.0
	if bestPair.Txns.H24.Sells > 0 {
		buySellRatio = float64(bestPair.Txns.H24.Buys) / float64(bestPair.Txns.H24.Sells)
	}

	// Calculate heuristic organic score and wash trade probability for now,
	// as DexScreener doesn't directly provide these advanced metrics.
	organicScore := 0.8
	if bestPair.Volume.M5 > 50000 {
		organicScore = 0.5 // Heavy volume might imply bot activity
	}
	washTradeProb := 0.1
	if buySellRatio > 5 || buySellRatio < 0.2 {
		washTradeProb = 0.8 // Suspiciously skewed
	}

	solPrice := fetchSolPrice()
	
	tokenPriceUsd := 0.0
	if p, err := strconv.ParseFloat(bestPair.PriceUsd, 64); err == nil {
		tokenPriceUsd = p
	}

	return models.TokenMetrics{
		Token:                tokenAddress,
		LiquidityUSD:         bestPair.Liquidity.Usd,
		MarketCap:            bestPair.Fdv,
		Volume5m:             bestPair.Volume.M5,
		LiquiditySOL:         bestPair.Liquidity.Usd / solPrice,
		PriceSOL:             tokenPriceUsd / solPrice,
		MarketCapSOL:         bestPair.Fdv / solPrice,
		Volume5mSOL:          bestPair.Volume.M5 / solPrice,
		BuySellRatio:         buySellRatio,
		OrganicScore:         organicScore,
		WashTradeProbability: washTradeProb,
	}, nil
}

func fetchSolPrice() float64 {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.dexscreener.com/latest/dex/tokens/So11111111111111111111111111111111111111112")
	if err != nil {
		return 150.0 // fallback
	}
	defer resp.Body.Close()

	var data DexScreenerResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && len(data.Pairs) > 0 {
		if pStr := data.Pairs[0].PriceUsd; pStr != "" {
			if p, err := strconv.ParseFloat(pStr, 64); err == nil && p > 0 {
				return p
			}
		}
	}
	return 150.0 // fallback
}
