//! Risk Manager
//! Max 1 position,1% risk per trade, 3-loss pause, 10% drawdown reduce

use crate::models::{BtcConfig, BtcTreasuryState, RiskAssessment};

pub struct RiskManager;

impl RiskManager {
    /// Assess current risk state
    pub fn assess(
        treasury: &BtcTreasuryState,
        active_positions: i32,
        loss_streak: i32,
        drawdown_pct: f64,
        capital_usdt: f64,
        cfg: &BtcConfig,
    ) -> RiskAssessment {
        let max_loss_usdt = capital_usdt * cfg.risk_per_trade_pct;
        let position_size_usdt = max_loss_usdt / cfg.stop_loss_pct.abs().max(0.001);

        let risk_level = Self::compute_risk_level(
            loss_streak,
            drawdown_pct,
            active_positions,
            cfg.max_positions,
        );

        let pause_trading = loss_streak >= cfg.max_consecutive_losses;
        let reduce_position = drawdown_pct > 0.10;

        let can_open_new = !pause_trading
            && active_positions < cfg.max_positions
            && drawdown_pct <= 0.10
            && treasury.trading_paused_until.is_empty()
            || Self::is_pause_expired(treasury);

        RiskAssessment {
            risk_per_trade_pct: cfg.risk_per_trade_pct * 100.0,
            position_size_usdt,
            max_loss_usdt,
            current_exposure_usdt: capital_usdt * cfg.max_exposure,
            active_positions,
            loss_streak,
            drawdown_pct,
            risk_level,
            pause_trading,
            reduce_position,
            can_open_new,
        }
    }

    fn compute_risk_level(
        loss_streak: i32,
        drawdown_pct: f64,
        active_positions: i32,
        max_positions: i32,
    ) -> String {
        let mut score = 0.0;

        if loss_streak >= 3 { score += 3.0; }
        else if loss_streak >= 2 { score += 1.5; }
        else if loss_streak >= 1 { score += 0.5; }

        if drawdown_pct > 0.10 { score += 3.0; }
        else if drawdown_pct > 0.05 { score += 2.0; }
        else if drawdown_pct > 0.02 { score += 1.0; }

        if active_positions >= max_positions { score += 2.0; }

        if score >= 6.0 { "CRITICAL".to_string() }
        else if score >= 4.0 { "HIGH".to_string() }
        else if score >= 2.0 { "MEDIUM".to_string() }
        else { "LOW".to_string() }
    }

    fn is_pause_expired(treasury:&BtcTreasuryState) -> bool {
        if treasury.trading_paused_until.is_empty() {
            return true;
        }
        if let Ok(paused_until) = chrono::DateTime::parse_from_rfc3339(&treasury.trading_paused_until) {
            chrono::Utc::now() > paused_until.with_timezone(&chrono::Utc)
        } else {
            true
        }
    }

    /// Calculate position size in quote currency given capital and risk params
    pub fn calc_position_size(
        capital_usdt: f64,
        entry_price: f64,
        stop_loss_pct: f64,
        risk_pct: f64,
    ) -> f64 {
        let risk_amount = capital_usdt * risk_pct;
        let sl_distance = stop_loss_pct.abs() / 100.0;
        if sl_distance > 0.0 && entry_price > 0.0 {
            risk_amount / sl_distance
        } else {
            0.0
        }
    }

    /// Should we pause trading?
    pub fn should_pause(loss_streak: i32, max_losses: i32) -> bool {
        loss_streak >= max_losses
    }

    /// Should we reduce position size?
    pub fn should_reduce(drawdown_pct: f64, threshold: f64) -> bool {
        drawdown_pct > threshold
    }
}