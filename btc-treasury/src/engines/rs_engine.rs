//! Relative Strength Engine
//! RS Score = Coin Return - BTC Return
//! Higher RS = coin outperforming BTC = better candidate

use crate::models::PairMetrics;

pub struct RSEngine;

impl RSEngine {
    /// Calculate Relative Strength score for a pair.
    /// RS Score = weighted average of (coin_return - btc_return) across timeframes
    pub fn calculate_rs(metrics: &PairMetrics) -> f64 {
        // Weight: 1h 35%, 4h 30%, 1d 25%, 15m 10%
        let rs_15m = metrics.rs_15m * 0.10;
        let rs_1h = metrics.rs_1h * 0.35;
        let rs_4h = metrics.rs_4h * 0.30;
        let rs_1d = metrics.rs_1d * 0.25;

        let total = rs_15m + rs_1h + rs_4h + rs_1d;

        // Normalize to 0-10 scale (multiply by 100 to convert % to score)
        // Typical RS range: -5% to +10% → score 0 to 10
        (total * 100.0).clamp(-50.0, 100.0)
    }

    /// Is RS rising? (1h RS > 4h RS suggests accelerating momentum)
    pub fn is_rs_rising(metrics: &PairMetrics) -> bool {
        metrics.rs_1h > metrics.rs_4h && metrics.rs_1h > 0.0
    }

    /// Score RS component (40% of total AI score)
    /// Returns 0-10 score
    pub fn score_component(metrics: &PairMetrics) -> f64 {
        let rs = Self::calculate_rs(metrics);
        // Map: rs score range roughly -50 to 100 → normalize to 0-10
        ((rs + 50.0) / 15.0).clamp(0.0, 10.0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fake_metrics(rs_15m: f64, rs_1h: f64, rs_4h: f64, rs_1d: f64) -> PairMetrics {
        PairMetrics {
            pair: "SOLBTC".into(),
            rs_15m,
            rs_1h,
            rs_4h,
            rs_1d,
            ..Default::default()
        }
    }

    #[test]
    fn test_rs_calculation() {
        let m = fake_metrics(0.02, 0.05, 0.03, 0.01);
        let score = RSEngine::calculate_rs(&m);
        // RS should be positive for positive returns
        assert!(score > 0.0);
    }

    #[test]
    fn test_rs_rising() {
        let rising = fake_metrics(0.01, 0.08, 0.05, 0.03);
        assert!(RSEngine::is_rs_rising(&rising));

        let falling = fake_metrics(0.01, 0.03, 0.08, 0.05);
        assert!(!RSEngine::is_rs_rising(&falling));
    }
}