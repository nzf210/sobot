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
            && Self::is_pause_expired(treasury);

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

    /// Calculate position size in quote currency given capital and risk params.
    /// The true loss = position_value * (|SL%|/100 + 2 * taker_fee) = capital * risk_pct.
    /// So: position_value = capital * risk_pct / (|SL%|/100 + 2 * taker_fee)
    pub fn calc_position_size(
        capital: f64,
        entry_price: f64,
        stop_loss_pct: f64,
        risk_pct: f64,
        taker_fee_pct: f64,
    ) -> f64 {
        let sl_distance = stop_loss_pct.abs() / 100.0;
        let round_trip_fee = taker_fee_pct * 2.0;
        let total_risk_per_unit = sl_distance + round_trip_fee;
        if total_risk_per_unit > 0.0 && entry_price > 0.0 {
            (capital * risk_pct) / total_risk_per_unit
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

    /// Compute minimum stop-loss width from ATR.
    /// Returns a negative percentage. SL = max_hard_limit(|atr_based|, |current_sl|).
    ///
    /// Formula: min_sl_pct = -(max(1.5 × ATR%, 0.8%))
    /// This prevents SL from being tighter than 1.5× the average 15m noise.
    pub fn min_sl_from_atr(close_price: f64, atr_14: f64) -> f64 {
        if close_price <= 0.0 || atr_14 <= 0.0 {
            return -0.8; // fallback floor
        }
        let atr_pct = (atr_14 / close_price) * 100.0;
        let min_width = (atr_pct * 1.5).max(0.8);
        -min_width
    }

    /// Clamp a dynamic_stop_loss to at least min_sl_from_atr width.
    /// Returns the more negative (wider) of the two, capped at -5.0%.
    pub fn clamp_sl(original_sl: f64, close_price: f64, atr_14: f64) -> f64 {
        let min_sl = Self::min_sl_from_atr(close_price, atr_14);
        original_sl.min(min_sl).max(-5.0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_can_open_new_respects_constraints() {
        let mut treasury = BtcTreasuryState::default();
        let cfg = BtcConfig {
            max_positions: 3,
            max_consecutive_losses: 3,
            risk_per_trade_pct: 0.01,
            stop_loss_pct: -0.02,
            max_exposure: 0.5,
            ..Default::default()
        };

        // Standard condition: should be able to open new position
        let res = RiskManager::assess(&treasury, 1, 0, 0.01, 10000.0, &cfg);
        assert!(res.can_open_new);
        assert_eq!(res.risk_level, "LOW");

        // Max positions reached: should NOT be able to open new position
        let res = RiskManager::assess(&treasury, 3, 0, 0.01, 10000.0, &cfg);
        assert!(!res.can_open_new);

        // High drawdown (>10%): should NOT be able to open new position
        let res = RiskManager::assess(&treasury, 1, 0, 0.12, 10000.0, &cfg);
        assert!(!res.can_open_new);

        // Paused trading (consecutive losses >= max_consecutive_losses): should NOT be able to open new position
        let res = RiskManager::assess(&treasury, 1, 3, 0.01, 10000.0, &cfg);
        assert!(!res.can_open_new);

        // Even if trading_paused_until has expired/is empty, max positions constraint must still block opening new positions
        treasury.trading_paused_until = "".to_string();
        let res = RiskManager::assess(&treasury, 3, 0, 0.01, 10000.0, &cfg);
        assert!(!res.can_open_new);
    }
}