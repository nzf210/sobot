//! Momentum Engine
//! Calculates: EMA alignment, MACD, RSI, Volume Growth, ATR Expansion

use crate::models::PairMetrics;

pub struct MomentumEngine;

impl MomentumEngine {
    /// Check if EMA is in bullish alignment: EMA20 > EMA50 > EMA200
    pub fn is_ema_bullish_aligned(metrics: &PairMetrics) -> bool {
        if metrics.ema_20 == 0.0 || metrics.ema_50 == 0.0 || metrics.ema_200 == 0.0 {
            return false;
        }
        metrics.ema_20 > metrics.ema_50 && metrics.ema_50 > metrics.ema_200
    }

    /// Check if EMA is in bearish alignment: EMA20 < EMA50 < EMA200
    pub fn is_ema_bearish_aligned(metrics: &PairMetrics) -> bool {
        if metrics.ema_20 == 0.0 || metrics.ema_50 == 0.0 || metrics.ema_200 == 0.0 {
            return false;
        }
        metrics.ema_20 < metrics.ema_50 && metrics.ema_50 < metrics.ema_200
    }

    /// Is MACD bullish? (MACD line > Signal line and histogram > 0)
    pub fn is_macd_bullish(metrics: &PairMetrics) -> bool {
        metrics.macd_line > metrics.macd_signal && metrics.macd_histogram > 0.0
    }

    /// Is MACD bearish?
    pub fn is_macd_bearish(metrics: &PairMetrics) -> bool {
        metrics.macd_line < metrics.macd_signal && metrics.macd_histogram < 0.0
    }

    /// Volume above average? (volume_growth > 0 means current vol > average)
    pub fn is_volume_above_average(metrics: &PairMetrics) -> bool {
        metrics.volume_growth > 0.0
    }

    /// ATR expansion detected? (ATR expanding = increased volatility)
    pub fn is_atr_expanding(metrics: &PairMetrics) -> bool {
        metrics.atr_expansion > 0.15 // 15% expansion threshold
    }

    /// Score trend strength component (20% of total AI score)
    /// Returns 0-10 score based on EMA alignment + MACD + momentum direction
    pub fn score_trend_component(metrics: &PairMetrics) -> f64 {
        let mut score: f64 = 0.0;

        // EMA alignment (0-4 points)
        if Self::is_ema_bullish_aligned(metrics) {
            score += 4.0;
        } else if Self::is_ema_bearish_aligned(metrics) {
            score -= 2.0; // bearish = negative for long bias
        }

        // MACD (0-3 points)
        if Self::is_macd_bullish(metrics) {
            score += 3.0;
        } else if Self::is_macd_bearish(metrics) {
            score -= 1.0;
        }

        // RSI (0-2 points)
        // RSI > 70 = overbought = negative
        // RSI 40-70 = neutral/good
        // RSI < 40 = potential oversold bounce
        if metrics.rsi_14 >= 40.0 && metrics.rsi_14 <= 60.0 {
            score += 2.0; // ideal range for continuation
        } else if metrics.rsi_14 > 60.0 && metrics.rsi_14 <= 70.0 {
            score += 1.0;
        } else if metrics.rsi_14 < 40.0 {
            score += 1.0; // oversold can mean bounce potential
        }

        // ATR expansion (0-1 point) — higher volatility can be good if aligned
        if Self::is_atr_expanding(metrics) {
            score += 1.0;
        }

        score.clamp(0.0_f64, 10.0)
    }

    /// Score volatility quality (10% of total AI score)
    /// Good volatility = enough movement to be tradable but not dangerous
    pub fn score_volatility_component(metrics: &PairMetrics) -> f64 {
        let atr_pct = if metrics.close_1h > 0.0 {
            metrics.atr_14 / metrics.close_1h
        } else {
            0.0
        };

        // Ideal ATR%: 1-5% (enough movement to capture 3-8% TP)
        // < 1% = too flat, > 10% = too dangerous
        if atr_pct >= 0.01 && atr_pct <= 0.05 {
            10.0
        } else if atr_pct > 0.05 && atr_pct <= 0.10 {
            7.0
        } else if atr_pct < 0.01 {
            3.0 // too quiet
        } else {
            4.0 // too volatile
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_ema_bullish() {
        let mut m = PairMetrics::default();
        m.ema_20 = 0.0010;
        m.ema_50 = 0.0009;
        m.ema_200 = 0.0008;
        assert!(MomentumEngine::is_ema_bullish_aligned(&m));
        assert!(!MomentumEngine::is_ema_bearish_aligned(&m));
    }

    #[test]
    fn test_macd_bullish() {
        let mut m = PairMetrics::default();
        m.macd_line = 0.00005;
        m.macd_signal = 0.00003;
        m.macd_histogram = 0.00002;
        assert!(MomentumEngine::is_macd_bullish(&m));
        assert!(!MomentumEngine::is_macd_bearish(&m));
    }
}