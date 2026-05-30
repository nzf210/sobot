use serde::{Deserialize, Serialize};

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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcTreasuryState {
    pub current_btc: f64,
    pub previous_btc: f64,
    pub btc_growth_7d: f64,
    pub btc_growth_30d: f64,
    pub stable_value: f64,
    pub usdt_balance: f64,
    pub last_update: String,
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
}

fn default_scanner_pairs() -> Vec<String> {
    vec!["BTCUSDT".to_string()]
}

impl Default for BtcConfig {
    fn default() -> Self {
        Self {
            enabled: false,
            llm_activation_threshold: 0.75,
            min_confidence: 0.80,
            max_exposure: 0.50,
            daily_loss_limit_btc: 0.0005,
            max_consecutive_losses: 3,
            safe_mode_volatility: 9.0,
            safe_mode_drawdown: 0.05,
            scanner_pairs: default_scanner_pairs(),
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
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BtcAdvisoryInput {
    pub market_data: BtcMarketData,
    pub treasury: BtcTreasuryState,
    pub open_positions: Vec<BtcAdvisoryPosition>,
    pub loss_streak: i32,
}
