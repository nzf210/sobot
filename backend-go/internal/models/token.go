package models

type TokenMetrics struct {
    Token string `json:"token"`
    LiquidityUSD float64 `json:"liquidity_usd"`
    LiquiditySOL float64 `json:"liquidity_sol"`
    PriceSOL float64 `json:"price_sol"`
    MarketCap float64 `json:"market_cap"`
    MarketCapSOL float64 `json:"market_cap_sol"`
    Volume5m float64 `json:"volume_5m"`
    Volume5mSOL float64 `json:"volume_5m_sol"`
    BuySellRatio float64 `json:"buy_sell_ratio"`
    OrganicScore float64 `json:"organic_score"`
    WashTradeProbability float64 `json:"wash_trade_probability"`
}