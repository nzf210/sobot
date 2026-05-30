package models

type TokenMetrics struct {
    Token string `json:"token"`
    LiquidityUSD float64 `json:"liquidity_usd"`
    MarketCap float64 `json:"market_cap"`
    Volume5m float64 `json:"volume_5m"`
    BuySellRatio float64 `json:"buy_sell_ratio"`
    OrganicScore float64 `json:"organic_score"`
    WashTradeProbability float64 `json:"wash_trade_probability"`
}