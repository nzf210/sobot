//! Relative Strength Engine
//! RS Score = Coin Return - BTC Return
//! Higher RS = coin outperforming BTC = better candidate

use crate::models::PairMetrics;

pub struct RSEngine;

impl RSEngine {
    /// Calculate Relative Strength score for a pair.
    /// RS Score = weighted average of (coin_return - btc_return) across timeframes.
    /// Input `metrics.rs_*` fields are in percentage-point space:
    ///   rs_1h = (coin_ret_1h - btc_ret_1h) * 100
    /// Weights: 1h=35%, 4h=30%, 1d=25%, 15m=10% (aligned with SKILL.md)
    pub fn calculate_rs(metrics: &PairMetrics) -> f64 {
        // Weighted composite RS in percentage-point space
        let total = metrics.rs_15m * 0.10
            + metrics.rs_1h  * 0.35
            + metrics.rs_4h  * 0.30
            + metrics.rs_1d  * 0.25;
        total
    }

    /// Is RS rising? (1h RS > 4h RS suggests accelerating momentum)
    pub fn is_rs_rising(metrics: &PairMetrics) -> bool {
        metrics.rs_1h > metrics.rs_4h && metrics.rs_1h > 0.0
    }

    /// Score RS component (40% of total AI score). Returns 0-10.
    ///
    /// Normalization: rs values are in %-point space. Typical range for
    /// liquid BTC-quote pairs: -3% to +3% per timeframe composite.
    /// - RS = 0  → score 5.0  (neutral, neither outperforming nor underperforming)
    /// - RS = +3 → score ~10  (strong outperformance → top score)
    /// - RS = -3 → score ~0   (strong underperformance → skip)
    ///
    /// Uses a linear mapping centered at 0 with half-span = 3%:
    ///   score = (rs + 3.0) / 6.0 × 10, clamped to [0, 10].
    /// This gives symmetry (RS=0 → 5.0) and is calibrated to real BTC-quote
    /// spot pairs where ±1% hourly RS outperformance is a significant signal.
    pub fn score_component(metrics: &PairMetrics) -> f64 {
        let rs = Self::calculate_rs(metrics);
        // Span: [-3, +3] maps to [0, 10]. Center: rs=0 → 5.0.
        let span = 3.0_f64;
        ((rs + span) / (2.0 * span) * 10.0).clamp(0.0, 10.0)
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

    #[test]
    fn test_score_neutral_maps_to_five() {
        // RS = 0 (coin matches BTC) should give neutral score 5.0
        let neutral = fake_metrics(0.0, 0.0, 0.0, 0.0);
        let score = RSEngine::score_component(&neutral);
        assert!((score - 5.0).abs() < 0.01, "Neutral RS should score 5.0, got {}", score);
    }

    #[test]
    fn test_score_strong_positive_rs() {
        // RS = +3 across all timeframes → should score near 10
        let strong = fake_metrics(3.0, 3.0, 3.0, 3.0);
        let score = RSEngine::score_component(&strong);
        assert!(score > 9.0, "Strong positive RS should score > 9.0, got {}", score);
    }
}