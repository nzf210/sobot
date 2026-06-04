package models

// Core models mapped from models.rs

type BtcMarketData struct {
	Pair                string  `json:"pair"`
	MarketRegime        string  `json:"market_regime"`
	TrendStrength       float64 `json:"trend_strength"`
	VolumeScore         float64 `json:"volume_score"`
	LiquidityScore      float64 `json:"liquidity_score"`
	SpreadScore         float64 `json:"spread_score"`
	VolatilityScore     float64 `json:"volatility_score"`
	BreakoutProbability float64 `json:"breakout_probability"`
	ReversalProbability float64 `json:"reversal_probability"`
	Confidence          float64 `json:"confidence"`
	ActiveStrategy      string  `json:"active_strategy"`
	PortfolioExposure   float64 `json:"portfolio_exposure"`
	DailyDrawdown       float64 `json:"daily_drawdown"`
}

type Ohlcv struct {
	OpenTime    int64   `json:"open_time"`
	Open        float64 `json:"open"`
	High        float64 `json:"high"`
	Low         float64 `json:"low"`
	Close       float64 `json:"close"`
	Volume      float64 `json:"volume"`
	QuoteVolume float64 `json:"quote_volume"`
}

func (o Ohlcv) Returns(prev Ohlcv) float64 {
	if prev.Close > 0.0 {
		return (o.Close - prev.Close) / prev.Close
	}
	return 0.0
}

type PairMetrics struct {
	Pair                 string  `json:"pair"`
	Close15m             float64 `json:"close_15m"`
	Close1h              float64 `json:"close_1h"`
	Close4h              float64 `json:"close_4h"`
	Close1d              float64 `json:"close_1d"`
	Volume15m            float64 `json:"volume_15m"`
	Volume1h             float64 `json:"volume_1h"`
	Volume4h             float64 `json:"volume_4h"`
	Volume1d             float64 `json:"volume_1d"`
	Atr14                float64 `json:"atr_14"`
	AtrAtr               float64 `json:"atr_atr"`
	Rsi14                float64 `json:"rsi_14"`
	Ema20                float64 `json:"ema_20"`
	Ema50                float64 `json:"ema_50"`
	Ema200               float64 `json:"ema_200"`
	MacdLine             float64 `json:"macd_line"`
	MacdSignal           float64 `json:"macd_signal"`
	MacdHistogram        float64 `json:"macd_histogram"`
	Vwap                 float64 `json:"vwap"`
	BidDepth             float64 `json:"bid_depth"`
	AskDepth             float64 `json:"ask_depth"`
	SpreadPct            float64 `json:"spread_pct"`
	BtcReturn15m         float64 `json:"btc_return_15m"`
	BtcReturn1h          float64 `json:"btc_return_1h"`
	BtcReturn4h          float64 `json:"btc_return_4h"`
	BtcReturn1d          float64 `json:"btc_return_1d"`
	Rs15m                float64 `json:"rs_15m"`
	Rs1h                 float64 `json:"rs_1h"`
	Rs4h                 float64 `json:"rs_4h"`
	Rs1d                 float64 `json:"rs_1d"`
	RsScore              float64 `json:"rs_score"`
	VolumeGrowth         float64 `json:"volume_growth"`
	AtrExpansion         float64 `json:"atr_expansion"`
	EmaBullishAlignment  bool    `json:"ema_bullish_alignment"`
	MacdBullish          bool    `json:"macd_bullish"`
	VolumeSpike          bool    `json:"volume_spike"`
	VolumeExpansion      bool    `json:"volume_expansion"`
	LiquidityGrowth      bool    `json:"liquidity_growth"`
	WashTradeDetected    bool    `json:"wash_trade_detected"`
	LowLiquidity         bool    `json:"low_liquidity"`
	WideSpread           bool    `json:"wide_spread"`
}

type AIScoringOutput struct {
	Pair            string             `json:"pair"`
	Score           float64            `json:"score"`
	Components      AIScoreComponents  `json:"components"`
	RankedPositions []RankedPair       `json:"ranked_positions"`
}

type AIScoreComponents struct {
	RelativeStrength   float64 `json:"relative_strength"`
	VolumeGrowth       float64 `json:"volume_growth"`
	TrendStrength      float64 `json:"trend_strength"`
	VolatilityQuality  float64 `json:"volatility_quality"`
	MarketStructure    float64 `json:"market_structure"`
}

type RankedPair struct {
	Pair           string  `json:"pair"`
	Score          float64 `json:"score"`
	RsScore        float64 `json:"rs_score"`
	VolumeScore    float64 `json:"volume_score"`
	TrendScore     float64 `json:"trend_score"`
	RiskScore      float64 `json:"risk_score"`
	Recommendation string  `json:"recommendation"`
}

type RiskAssessment struct {
	RiskPerTradePct     float64 `json:"risk_per_trade_pct"`
	PositionSizeUsdt    float64 `json:"position_size_usdt"`
	MaxLossUsdt         float64 `json:"max_loss_usdt"`
	CurrentExposureUsdt float64 `json:"current_exposure_usdt"`
	ActivePositions     int     `json:"active_positions"`
	LossStreak          int     `json:"loss_streak"`
	DrawdownPct         float64 `json:"drawdown_pct"`
	RiskLevel           string  `json:"risk_level"`
	PauseTrading        bool    `json:"pause_trading"`
	ReducePosition      bool    `json:"reduce_position"`
	CanOpenNew          bool    `json:"can_open_new"`
}

type ExecutionPlan struct {
	Action           string   `json:"action"`
	Pair             string   `json:"pair"`
	Confidence       float64  `json:"confidence"`
	EntryPrice       float64  `json:"entry_price"`
	StopLossPrice    float64  `json:"stop_loss_price"`
	TakeProfitPrice  float64  `json:"take_profit_price"`
	PositionSizeUsdt float64  `json:"position_size_usdt"`
	RiskPct          float64  `json:"risk_pct"`
	Reasons          []string `json:"reasons"`
	TpPct            float64  `json:"tp_pct"`
	SlPct            float64  `json:"sl_pct"`
	Timestamp        string   `json:"timestamp"`
}

type TradingSignals struct {
	Pair                 string `json:"pair"`
	RsRising             bool   `json:"rs_rising"`
	Ema20AboveEma50      bool   `json:"ema20_above_ema50"`
	Ema50AboveEma200     bool   `json:"ema50_above_ema200"`
	MacdBullish          bool   `json:"macd_bullish"`
	VolumeAboveAverage   bool   `json:"volume_above_average"`
	AllAligned           bool   `json:"all_aligned"`
	Timestamp            string `json:"timestamp"`
}

type BtcTreasuryState struct {
	CurrentBtc         float64 `json:"current_btc"`
	PreviousBtc        float64 `json:"previous_btc"`
	BtcGrowth7d        float64 `json:"btc_growth_7d"`
	BtcGrowth30d       float64 `json:"btc_growth_30d"`
	StableValue        float64 `json:"stable_value"`
	UsdtBalance        float64 `json:"usdt_balance"`
	LastUpdate         string  `json:"last_update"`
	BtcTreasuryVault   float64 `json:"btc_treasury_vault"`
	CompoundBalance    float64 `json:"compound_balance"`
	TotalTrades        int     `json:"total_trades"`
	WinningTrades      int     `json:"winning_trades"`
	LosingTrades       int     `json:"losing_trades"`
	TradingPausedUntil string  `json:"trading_paused_until"`
	ConsecutiveLosses  int     `json:"consecutive_losses"`
}

type FullBtcAdvisory struct {
	Recommendation    string   `json:"recommendation"`
	Confidence        float64  `json:"confidence"`
	RiskLevel         string   `json:"risk_level"`
	TreasuryMode      string   `json:"treasury_mode"`
	Reason            string   `json:"reason"`
	Warnings          []string `json:"warnings"`
	MarketRegime      string   `json:"market_regime"`
	OpportunityScore  float64  `json:"opportunity_score"`
	BypassQuant       bool     `json:"bypass_quant"`
	Timestamp         string   `json:"timestamp"`
	DynamicTakeProfit float64  `json:"dynamic_take_profit"`
	DynamicStopLoss   float64  `json:"dynamic_stop_loss"`
	TpReason          string   `json:"tp_reason"`
	SlReason          string   `json:"sl_reason"`
}

type BtcDecisionRecord struct {
	Timestamp      string           `json:"timestamp"`
	MarketData     BtcMarketData    `json:"market_data"`
	TreasuryBefore BtcTreasuryState `json:"treasury_before"`
	TreasuryAfter  BtcTreasuryState `json:"treasury_after"`
	Advisory       FullBtcAdvisory  `json:"advisory"`
	ActionTaken    string           `json:"action_taken"`
}

type BtcConfig struct {
	Enabled                bool     `json:"enabled"`
	LlmActivationThreshold float64  `json:"llm_activation_threshold"`
	MinConfidence          float64  `json:"min_confidence"`
	MaxExposure            float64  `json:"max_exposure"`
	DailyLossLimitBtc      float64  `json:"daily_loss_limit_btc"`
	MaxConsecutiveLosses   int      `json:"max_consecutive_losses"`
	SafeModeVolatility     float64  `json:"safe_mode_volatility"`
	SafeModeDrawdown       float64  `json:"safe_mode_drawdown"`
	ScannerPairs           []string `json:"scanner_pairs"`
	TakeProfitPct          float64  `json:"take_profit_pct"`
	StopLossPct            float64  `json:"stop_loss_pct"`
	TrailingTpPct          float64  `json:"trailing_tp_pct"`
	UseTrailing            bool     `json:"use_trailing"`
	MaxPositions           int      `json:"max_positions"`
	RiskPerTradePct        float64  `json:"risk_per_trade_pct"`
	InitialCapitalUsdt     float64  `json:"initial_capital_usdt"`
	MinScoreThreshold      float64  `json:"min_score_threshold"`
	CompoundPct            float64  `json:"compound_pct"`
	TreasuryPct            float64  `json:"treasury_pct"`
	DryRun                 bool     `json:"dry_run"`
	TakerFeePct            float64  `json:"taker_fee_pct"`
}

type BtcAdvisoryPosition struct {
	ID             string  `json:"id"`
	EntryPrice     float64 `json:"entry_price"`
	CurrentPrice   float64 `json:"current_price"`
	Size           float64 `json:"size"`
	PnlBtc         float64 `json:"pnl_btc"`
	EntryTime      string  `json:"entry_time"`
	Side           string  `json:"side"`
	TakeProfitPct  float64 `json:"take_profit_pct"`
	StopLossPct    float64 `json:"stop_loss_pct"`
	TrailingTpPct  float64 `json:"trailing_tp_pct"`
	UseTrailing    bool    `json:"use_trailing"`
	LlmTpReason    string  `json:"llm_tp_reason"`
	LlmSlReason    string  `json:"llm_sl_reason"`
	LlmConfidence  float64 `json:"llm_confidence"`
	HighestPrice   float64 `json:"highest_price"`
}

type BtcAdvisoryInput struct {
	MarketData     BtcMarketData         `json:"market_data"`
	Treasury       BtcTreasuryState      `json:"treasury"`
	OpenPositions  []BtcAdvisoryPosition `json:"open_positions"`
	LossStreak     int                   `json:"loss_streak"`
	AiScore        *float64              `json:"ai_score,omitempty"`
	RiskAssessment *RiskAssessment       `json:"risk_assessment,omitempty"`
	PairMetrics    *PairMetrics          `json:"pair_metrics,omitempty"`
}

type ExchangeBalance struct {
	Asset  string  `json:"asset"`
	Free   float64 `json:"free"`
	Locked float64 `json:"locked"`
}

type ExchangeOrderResult struct {
	OrderID   string  `json:"order_id"`
	Status    string  `json:"status"`
	FilledQty float64 `json:"filled_qty"`
}

type Orderbook struct {
	Bids [][2]float64 `json:"bids"` // Array of [price, size]
	Asks [][2]float64 `json:"asks"`
}
