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
		ChainID string `json:"chainId"`
		Liquidity struct {
			Usd float64 `json:"usd"`
		} `json:"liquidity"`
		Fdv       float64 `json:"fdv"`
		MarketCap float64 `json:"marketCap"`
		Volume struct {
			M5  float64 `json:"m5"`
			H1  float64 `json:"h1"`
			H6  float64 `json:"h6"`
			H24 float64 `json:"h24"`
		} `json:"volume"`
		Txns struct {
			M5 struct {
				Buys  int `json:"buys"`
				Sells int `json:"sells"`
			} `json:"m5"`
			H1 struct {
				Buys  int `json:"buys"`
				Sells int `json:"sells"`
			} `json:"h1"`
			H24 struct {
				Buys  int `json:"buys"`
				Sells int `json:"sells"`
			} `json:"h24"`
		} `json:"txns"`
		PriceUsd     string  `json:"priceUsd"`
		PriceChange  struct {
			M5  float64 `json:"m5"`
			H1  float64 `json:"h1"`
			H6  float64 `json:"h6"`
			H24 float64 `json:"h24"`
		} `json:"priceChange"`
		PairCreatedAt int64 `json:"pairCreatedAt"` // Unix ms
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

	// Pick the pair with highest liquidity (most reliable)
	bestPair := data.Pairs[0]
	for _, p := range data.Pairs {
		if p.Liquidity.Usd > bestPair.Liquidity.Usd {
			bestPair = p
		}
	}

	// Buy/sell ratio from 5m txns (most recent signal)
	buySellRatio := 1.0
	if bestPair.Txns.M5.Sells > 0 {
		buySellRatio = float64(bestPair.Txns.M5.Buys) / float64(bestPair.Txns.M5.Sells)
	} else if bestPair.Txns.M5.Buys > 0 {
		buySellRatio = 5.0 // all buys, no sells — cap at 5
	}

	// Organic score: heuristic based on volume, price change, and buy pressure
	organicScore := 70.0 // base score
	if bestPair.Volume.M5 > 100000 {
		organicScore -= 20.0 // Suspiciously high 5m volume
	}
	if bestPair.PriceChange.M5 > 50.0 {
		organicScore -= 15.0 // 50%+ pump in 5m is suspicious
	}
	if bestPair.PriceChange.M5 < -20.0 {
		organicScore -= 10.0 // Dumping
	}
	if buySellRatio > 8 {
		organicScore -= 10.0 // Extreme buy skew = bot/shill
	}
	if bestPair.Txns.M5.Buys+bestPair.Txns.M5.Sells < 5 {
		organicScore -= 15.0 // Too few txns in 5m
	}
	if organicScore < 0 {
		organicScore = 0
	}
	if organicScore > 100 {
		organicScore = 100
	}

	// Wash trade probability
	washTradeProb := 0.1
	if buySellRatio > 8 || buySellRatio < 0.2 {
		washTradeProb = 0.7
	} else if buySellRatio > 5 || buySellRatio < 0.4 {
		washTradeProb = 0.4
	}
	// Very high 5m vol with few txns = wash trade
	if bestPair.Volume.M5 > 50000 && (bestPair.Txns.M5.Buys+bestPair.Txns.M5.Sells) < 10 {
		washTradeProb = 0.85
	}

	// Use circulating market cap when available (DexScreener provides both 'marketCap' and 'fdv')
	// FDV = totalSupply * price, which is massively inflated for tokens with 1B+ supply
	// marketCap = circulating supply * price, much more realistic
	mcap := bestPair.MarketCap
	if mcap <= 0 {
		mcap = bestPair.Fdv
	}
	// Cap at a reasonable value for new meme tokens (max ~$1M USD = ~6600 SOL)
	if mcap > 1_000_000 && (bestPair.Liquidity.Usd < 20000) {
		mcap = bestPair.Liquidity.Usd * 10 // rough estimate based on liquidity
	}

	solPrice := fetchSolPrice()

	tokenPriceUsd := 0.0
	if p, err := strconv.ParseFloat(bestPair.PriceUsd, 64); err == nil {
		tokenPriceUsd = p
	}

	pairCreatedAt := time.Time{}
	pairAgeSec := int64(0)
	if bestPair.PairCreatedAt > 0 {
		pairCreatedAt = time.UnixMilli(bestPair.PairCreatedAt)
		pairAgeSec = int64(time.Since(pairCreatedAt).Seconds())
	}

	return models.TokenMetrics{
		Token:                tokenAddress,
		LiquidityUSD:         bestPair.Liquidity.Usd,
		LiquiditySOL:         bestPair.Liquidity.Usd / solPrice,
		PriceUSD:             tokenPriceUsd,
		PriceSOL:             tokenPriceUsd / solPrice,
		MarketCap:            mcap,
		MarketCapSOL:         mcap / solPrice,
		Volume5m:             bestPair.Volume.M5,
		Volume5mSOL:          bestPair.Volume.M5 / solPrice,
		Volume1h:             bestPair.Volume.H1,
		Volume6h:             bestPair.Volume.H6,
		Volume24h:            bestPair.Volume.H24,
		BuySellRatio:         buySellRatio,
		Buys5m:               bestPair.Txns.M5.Buys,
		Sells5m:              bestPair.Txns.M5.Sells,
		Buys1h:               bestPair.Txns.H1.Buys,
		Sells1h:              bestPair.Txns.H1.Sells,
		PriceChange5m:        bestPair.PriceChange.M5,
		PriceChange1h:        bestPair.PriceChange.H1,
		PriceChange6h:        bestPair.PriceChange.H6,
		PriceChange24h:       bestPair.PriceChange.H24,
		OrganicScore:         organicScore,
		WashTradeProbability: washTradeProb,
		PairCreatedAt:        pairCreatedAt,
		PairAgeSec:           pairAgeSec,
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

