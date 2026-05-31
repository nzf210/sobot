//! AI Scoring Model
//! Weights: 40% RS, 25% Volume, 20% Trend, 10% Volatility, 5% Market Structure
//! Output: ranked list of pairs with scores

use crate::engines::rs_engine::RSEngine;
use crate::engines::momentum_engine::MomentumEngine;
use crate::engines::volume_engine::VolumeEngine;
use crate::models::{AIScoringOutput, AIScoreComponents, PairMetrics, RankedPair, RiskAssessment};

pub struct AIScoringEngine;

impl AIScoringEngine {
    /// Score a single pair and return detailed breakdown
    pub fn score_pair(metrics: &PairMetrics, risk: &RiskAssessment) -> AIScoringOutput {
        let rs = RSEngine::score_component(metrics);
        let vol = VolumeEngine::score_component(metrics);
        let trend = MomentumEngine::score_trend_component(metrics);
        let vol_quality = MomentumEngine::score_volatility_component(metrics);

        // Market structure: EMA alignment + RS rising = good structure
        let structure = Self::score_market_structure(metrics);

        // Weighted total (0-10)
        let total = rs * 0.40 + vol * 0.25 + trend * 0.20 + vol_quality * 0.10 + structure * 0.05;

        let components = AIScoreComponents {
            relative_strength: rs,
            volume_growth: vol,
            trend_strength: trend,
            volatility_quality: vol_quality,
            market_structure: structure,
        };

        let recommendation = if total >= 8.0 {
            "APPROVE"
        } else if total >= 6.0 {
            "MONITOR"
        } else {
            "REJECT"
        };

        AIScoringOutput {
            pair: metrics.pair.clone(),
            score: (total * 10.0).round() / 10.0, // 0-10 scale
            components,
            ranked_positions: vec![RankedPair {
                pair: metrics.pair.clone(),
                score: total,
                rs_score: rs,
                volume_score: vol,
                trend_score: trend,
                risk_score: 0.0, // filled by risk manager
                recommendation: recommendation.to_string(),
            }],
        }
    }

    /// Score market structure component (5% of total AI score)
    fn score_market_structure(metrics: &PairMetrics) -> f64 {
        let mut score: f64 = 0.0;

        // EMA in order (bullish or neutral)
        if metrics.ema_20 > 0.0 && metrics.ema_50 > 0.0 {
            score += 3.0;
        }

        // RS rising = good structure
        if RSEngine::is_rs_rising(metrics) {
            score += 4.0;
        }

        // Low spread = good structure
        if metrics.spread_pct < 0.1 {
            score += 3.0;
        } else if metrics.spread_pct < 0.3 {
            score += 1.5;
        }

        score.clamp(0.0_f64, 10.0)
    }

    /// Rank multiple pairs and return sorted list (highest score first)
    pub fn rank_pairs(
        scorings: Vec<AIScoringOutput>,
        risk:&RiskAssessment,
        min_score: f64,
    ) -> Vec<RankedPair> {
        let mut ranked: Vec<RankedPair> = scorings
            .into_iter()
            .filter(|s| s.score >= min_score)
            .map(|s| {
                let mut pair = RankedPair {
                    pair: s.pair,
                    score: s.score,
                    rs_score: s.components.relative_strength,
                    volume_score: s.components.volume_growth,
                    trend_score: s.components.trend_strength,
                    risk_score: 0.0,
                    recommendation: if s.score >= 8.0 {
                        "APPROVE".to_string()
                    } else if s.score >= 6.0 {
                        "MONITOR".to_string()
                    } else {
                        "REJECT".to_string()
                    },
                };
                pair.risk_score = if risk.risk_level == "LOW" || risk.risk_level == "MEDIUM" {
                    8.0
                } else {
                    4.0
                };
                pair
            })
            .collect();

        ranked.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        ranked
    }
}