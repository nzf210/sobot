#![allow(dead_code)]
use serde::{Deserialize, Serialize};

// ── Core Market Data ──────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcMarketData {
    #[serde(default = "default_btc_pair")]
    pub pair: String,
    pub market_regime: String,
    pub trend_strength: f64,
    pub volume_score: f64,
    pub liquidity_score: f64,
    pub spread_score: f64,
    pub volatility_score: f64,
    pub breakout_probability: f64,
    pub reversal_probability: f64,
    pub confidence: f64,
    pub active_strategy: String,
    pub portfolio_exposure: f64,
    pub daily_drawdown: f64,
}

fn default_btc_pair() -> String {
    "BTCUSDT".into()
}

// ── OHLCV + Technicals ───────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct Ohlcv {
    pub open_time: i64,
    pub open: f64,
    pub high: f64,
    pub low: f64,
    pub close: f64,
    pub volume: f64,
    pub quote_volume: f64,
}

impl Ohlcv {
    pub fn returns(&self, prev: &Ohlcv) -> f64 {
        if prev.close > 0.0 {
            (self.close - prev.close) / prev.close
        } else {
            0.0
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PairMetrics {
    pub pair: String,
    // OHLCV
    pub close_15m: f64,
    pub close_1h: f64,
    pub close_4h: f64,
    pub close_1d: f64,
    pub volume_15m: f64,
    pub volume_1h: f64,
    pub volume_4h: f64,
    pub volume_1d: f64,
    // ATR
    pub atr_14: f64,
    pub atr_atr: f64,
    // RSI
    pub rsi_14: f64,
    // EMA
    pub ema_20: f64,
    pub ema_50: f64,
    pub ema_200: f64,
    // MACD
    pub macd_line: f64,
    pub macd_signal: f64,
    pub macd_histogram: f64,
    // VWAP
    pub vwap: f64,
    // Orderbook
    pub bid_depth: f64,
    pub ask_depth: f64,
    pub spread_pct: f64,
    // BTC return (for RS calculation)
    pub btc_return_15m: f64,
    pub btc_return_1h: f64,
    pub btc_return_4h: f64,
    pub btc_return_1d: f64,
    // Relative Strength
    pub rs_15m: f64,
    pub rs_1h: f64,
    pub rs_4h: f64,
    pub rs_1d: f64,
    pub rs_score: f64,
    // Momentum
    pub volume_growth: f64,
    pub atr_expansion: f64,
    pub ema_bullish_alignment: bool,
    pub macd_bullish: bool,
    // Volume
    pub volume_spike: bool,
    pub volume_expansion: bool,
    pub liquidity_growth: bool,
    pub wash_trade_detected: bool,
    // Quality flags
    pub low_liquidity: bool,
    pub wide_spread: bool,
}

impl Default for PairMetrics {
    fn default() -> Self {
        Self {
            pair: String::new(),
            close_15m: 0.0,
            close_1h: 0.0,
            close_4h: 0.0,
            close_1d: 0.0,
            volume_15m: 0.0,
            volume_1h: 0.0,
            volume_4h: 0.0,
            volume_1d: 0.0,
            atr_14: 0.0,
            atr_atr: 0.0,
            rsi_14: 50.0,
            ema_20: 0.0,
            ema_50: 0.0,
            ema_200: 0.0,
            macd_line: 0.0,
            macd_signal: 0.0,
            macd_histogram: 0.0,
            vwap: 0.0,
            bid_depth: 0.0,
            ask_depth: 0.0,
            spread_pct: 0.0,
            btc_return_15m: 0.0,
            btc_return_1h: 0.0,
            btc_return_4h: 0.0,
            btc_return_1d: 0.0,
            rs_15m: 0.0,
            rs_1h: 0.0,
            rs_4h: 0.0,
            rs_1d: 0.0,
            rs_score: 0.0,
            volume_growth: 0.0,
            atr_expansion: 0.0,
            ema_bullish_alignment: false,
            macd_bullish: false,
            volume_spike: false,
            volume_expansion: false,
            liquidity_growth: false,
            wash_trade_detected: false,
            low_liquidity: false,
            wide_spread: false,
        }
    }
}

// ── AI Scoring Output ────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AIScoringOutput {
    pub pair: String,
    pub score: f64,
    pub components: AIScoreComponents,
    pub ranked_positions: Vec<RankedPair>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AIScoreComponents {
    pub relative_strength: f64,  // 40%
    pub volume_growth: f64,       // 25%
    pub trend_strength: f64,      // 20%
    pub volatility_quality: f64,  // 10%
    pub market_structure: f64,    // 5%
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RankedPair {
    pub pair: String,
    pub score: f64,
    pub rs_score: f64,
    pub volume_score: f64,
    pub trend_score: f64,
    pub risk_score: f64,
    pub recommendation: String,
}

// ── Risk Assessment ──────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RiskAssessment {
    pub risk_per_trade_pct: f64,
    pub position_size_usdt: f64,
    pub max_loss_usdt: f64,
    pub current_exposure_usdt: f64,
    pub active_positions: i32,
    pub loss_streak: i32,
    pub drawdown_pct: f64,
    pub risk_level: String, // LOW, MEDIUM, HIGH, CRITICAL
    pub pause_trading: bool,
    pub reduce_position: bool,
    pub can_open_new: bool,
}

// ── Execution Plan ────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExecutionPlan {
    pub action: String,        // BUY, SELL, DO_NOTHING
    pub pair: String,
    pub confidence: f64,
    pub entry_price: f64,
    pub stop_loss_price: f64,
    pub take_profit_price: f64,
    pub position_size_usdt: f64,
    pub risk_pct: f64,
    pub reasons: Vec<String>,
    pub tp_pct: f64,
    pub sl_pct: f64,
    pub timestamp: String,
}

// ── Trading Signals ─────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradingSignals {
    pub pair: String,
    pub rs_rising: bool,
    pub ema20_above_ema50: bool,
    pub ema50_above_ema200: bool,
    pub macd_bullish: bool,
    pub volume_above_average: bool,
    pub all_aligned: bool,
    pub timestamp: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcTreasuryState {
    pub current_btc: f64,
    pub previous_btc: f64,
    pub btc_growth_7d: f64,
    pub btc_growth_30d: f64,
    pub stable_value: f64,
    pub usdt_balance: f64,
    pub last_update: String,
    // BTC Accumulation tracking
    #[serde(default)]
    pub btc_treasury_vault: f64,
    #[serde(default)]
    pub compound_balance: f64,
    #[serde(default)]
    pub total_trades: i32,
    #[serde(default)]
    pub winning_trades: i32,
    #[serde(default)]
    pub losing_trades: i32,
    #[serde(default)]
    pub trading_paused_until: String,
    /// Track consecutive losses for auto-pause logic
    #[serde(default)]
    pub consecutive_losses: i32,
}

impl Default for BtcTreasuryState {
    fn default() -> Self {
        Self {
            current_btc: 0.0,
            previous_btc: 0.0,
            btc_growth_7d: 0.0,
            btc_growth_30d: 0.0,
            stable_value: 0.0,
            usdt_balance: 0.0,
            last_update: String::new(),
            btc_treasury_vault: 0.0,
            compound_balance: 0.0,
            total_trades: 0,
            winning_trades: 0,
            losing_trades: 0,
            trading_paused_until: String::new(),
            consecutive_losses: 0,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FullBtcAdvisory {
    pub recommendation: String,
    pub confidence: f64,
    pub risk_level: String,
    pub treasury_mode: String,
    pub reason: String,
    pub warnings: Vec<String>,
    pub market_regime: String,
    pub opportunity_score: f64,
    pub bypass_quant: bool,
    pub timestamp: String,
    // Dynamic TP/SL set by LLM
    #[serde(default)]
    pub dynamic_take_profit: f64,
    #[serde(default)]
    pub dynamic_stop_loss: f64,
    #[serde(default)]
    pub tp_reason: String,
    #[serde(default)]
    pub sl_reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcDecisionRecord {
    pub timestamp: String,
    pub market_data: BtcMarketData,
    pub treasury_before: BtcTreasuryState,
    pub treasury_after: BtcTreasuryState,
    pub advisory: FullBtcAdvisory,
    pub action_taken: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcConfig {
    pub enabled: bool,
    pub llm_activation_threshold: f64,
    pub min_confidence: f64,
    pub max_exposure: f64,
    pub daily_loss_limit_btc: f64,
    pub max_consecutive_losses: i32,
    pub safe_mode_volatility: f64,
    pub safe_mode_drawdown: f64,
    #[serde(default = "default_scanner_pairs")]
    pub scanner_pairs: Vec<String>,
    // Default TP/SL (fallback when LLM doesn't override)
    #[serde(default = "default_take_profit_pct")]
    pub take_profit_pct: f64,
    #[serde(default = "default_stop_loss_pct")]
    pub stop_loss_pct: f64,
    #[serde(default = "default_trailing_tp_pct")]
    pub trailing_tp_pct: f64,
    #[serde(default)]
    pub use_trailing: bool,
    // New fields for BTC accumulation mode
    #[serde(default = "default_max_positions")]
    pub max_positions: i32,
    #[serde(default = "default_risk_per_trade")]
    pub risk_per_trade_pct: f64,
    #[serde(default = "default_initial_capital")]
    pub initial_capital_usdt: f64,
    #[serde(default = "default_min_score")]
    pub min_score_threshold: f64,
    #[serde(default = "default_compound_pct")]
    pub compound_pct: f64,
    #[serde(default = "default_treasury_pct")]
    pub treasury_pct: f64,
    #[serde(default)]
    pub dry_run: bool,
    /// Taker fee rate (decimal, e.g. 0.001 = 0.1%).
    /// Applied at both entry and exit → round-trip = 2× this.
    #[serde(default = "default_taker_fee")]
    pub taker_fee_pct: f64,
}

fn default_take_profit_pct() -> f64 { 4.0 }
fn default_stop_loss_pct() -> f64 { -0.8 }
fn default_trailing_tp_pct() -> f64 { 3.0 }
fn default_scanner_pairs() -> Vec<String> { vec!["BTCUSDT".to_string()] }
fn default_max_positions() -> i32 { 1 }
fn default_risk_per_trade() -> f64 { 0.01 }
fn default_initial_capital() -> f64 { 50.0 }
fn default_min_score() -> f64 { 80.0 }
fn default_compound_pct() -> f64 { 0.50 }
fn default_treasury_pct() -> f64 { 0.50 }
fn default_taker_fee() -> f64 { 0.001 }

impl Default for BtcConfig {
    fn default() -> Self {
        Self {
            enabled: false,
            llm_activation_threshold: 0.85,
            min_confidence: 0.80,
            max_exposure: 0.50,
            daily_loss_limit_btc: 0.0005,
            max_consecutive_losses: 3,
            safe_mode_volatility: 9.0,
            safe_mode_drawdown: 0.05,
            scanner_pairs: default_scanner_pairs(),
            take_profit_pct: 4.0,
            stop_loss_pct: -0.8,
            trailing_tp_pct: 3.0,
            use_trailing: true,
            max_positions: 1,
            risk_per_trade_pct: 0.01,
            initial_capital_usdt: 50.0,
            min_score_threshold: 80.0,
            compound_pct: 0.50,
            treasury_pct: 0.50,
            dry_run: false,
            taker_fee_pct: 0.001,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcAdvisoryPosition {
    pub id: String,
    pub entry_price: f64,
    pub current_price: f64,
    pub size: f64,
    pub pnl_btc: f64,
    pub entry_time: String,
    #[serde(default)]
    pub side: String,
    // Dynamic TP/SL set by LLM at entry time
    #[serde(default)]
    pub take_profit_pct: f64,   // override config if set (>0), e.g. 25.5 means 25.5%
    #[serde(default)]
    pub stop_loss_pct: f64,     // override config if set (<0), e.g. -8.5 means -8.5%
    #[serde(default)]
    pub trailing_tp_pct: f64,   // trailing TP percentage
    #[serde(default)]
    pub use_trailing: bool,     // enable smart trailing
    #[serde(default)]
    pub llm_tp_reason: String,  // LLM reasoning for TP
    #[serde(default)]
    pub llm_sl_reason: String,  // LLM reasoning for SL
    #[serde(default)]
    pub llm_confidence: f64,     // LLM confidence at entry
    #[serde(default)]
    pub highest_price: f64,    // track peak price for trailing
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcAdvisoryInput {
    pub market_data: BtcMarketData,
    pub treasury: BtcTreasuryState,
    pub open_positions: Vec<BtcAdvisoryPosition>,
    pub loss_streak: i32,
    /// AI scoring from technical indicators (OHLCV-based), 0-100
    #[serde(default)]
    pub ai_score: Option<f64>,
    /// Risk assessment from RiskManager
    #[serde(default)]
    pub risk_assessment: Option<RiskAssessment>,
    /// PairMetrics computed from OHLCV candles
    #[serde(default)]
    pub pair_metrics: Option<PairMetrics>,
}
