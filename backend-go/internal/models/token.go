package models

import "time"

type TokenMetrics struct {
	Token                string    `json:"token"`
	LiquidityUSD         float64   `json:"liquidity_usd"`
	LiquiditySOL         float64   `json:"liquidity_sol"`
	PriceSOL             float64   `json:"price_sol"`
	PriceUSD             float64   `json:"price_usd"`
	MarketCap            float64   `json:"market_cap"`
	MarketCapSOL         float64   `json:"market_cap_sol"`
	Volume5m             float64   `json:"volume_5m"`
	Volume5mSOL          float64   `json:"volume_5m_sol"`
	Volume1h             float64   `json:"volume_1h"`
	Volume6h             float64   `json:"volume_6h"`
	Volume24h            float64   `json:"volume_24h"`
	BuySellRatio         float64   `json:"buy_sell_ratio"`
	Buys5m               int       `json:"buys_5m"`
	Sells5m              int       `json:"sells_5m"`
	Buys1h               int       `json:"buys_1h"`
	Sells1h              int       `json:"sells_1h"`
	PriceChange5m        float64   `json:"price_change_5m"`
	PriceChange1h        float64   `json:"price_change_1h"`
	PriceChange6h        float64   `json:"price_change_6h"`
	PriceChange24h       float64   `json:"price_change_24h"`
	OrganicScore         float64   `json:"organic_score"`
	WashTradeProbability float64   `json:"wash_trade_probability"`
	PairCreatedAt        time.Time `json:"pair_created_at"`
	PairAgeSec           int64     `json:"pair_age_sec"` // seconds since pair creation
}