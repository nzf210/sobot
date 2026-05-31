//! Volume Engine
//! Detects: Volume Spike, Volume Expansion, Liquidity Growth, Wash Trading

use crate::models::PairMetrics;

pub struct VolumeEngine;

impl VolumeEngine {
    /// Volume spike: current volume > 2x average
    pub fn is_volume_spike(metrics: &PairMetrics) -> bool {
        metrics.volume_growth > 1.0 // volume_growth > 1.0 means > 2x average
    }

    /// Volume expansion: volume growing across timeframes
    pub fn is_volume_expansion(metrics: &PairMetrics) -> bool {
        metrics.volume_expansion
    }

    /// Liquidity growth: bid/ask depth increasing
    pub fn is_liquidity_growing(metrics: &PairMetrics) -> bool {
        metrics.liquidity_growth
    }

    /// Detect wash trading: volume high but spread also high + no price movement
    /// Returns true if suspicious wash trading detected
    pub fn is_wash_trade(metrics: &PairMetrics) -> bool {
        // Suspicious: high volume but very wide spread and no meaningful price change
        let wide_spread = metrics.spread_pct > 0.5; // > 0.5% spread
        let low_movement = metrics.rs_score.abs() < 1.0; // barely moving
        let high_volume = metrics.volume_growth > 0.8; // volume picking up

        wide_spread && low_movement && high_volume
    }

    /// Is pair eligible for trading? (not low liquidity, not wide spread, not wash trade)
    pub fn is_pair_eligible(metrics: &PairMetrics) -> bool {
        !metrics.low_liquidity && !metrics.wide_spread && !Self::is_wash_trade(metrics)
    }

    /// Score volume component (25% of total AI score)
    /// Returns 0-10 score
    pub fn score_component(metrics: &PairMetrics) -> f64 {
        if !Self::is_pair_eligible(metrics) {
            return 0.0;
        }

        let mut score: f64 = 0.0;

        // Volume spike (0-5 points)
        if Self::is_volume_spike(metrics) {
            score += 5.0;
        } else if metrics.volume_growth > 0.5 {
            score += 3.0;
        } else if metrics.volume_growth > 0.0 {
            score += 1.0;
        }

        // Volume expansion (0-3 points)
        if Self::is_volume_expansion(metrics) {
            score += 3.0;
        }

        // Liquidity growth (0-2 points)
        if Self::is_liquidity_growing(metrics) {
            score += 2.0;
        }

        score.clamp(0.0, 10.0)
    }
}