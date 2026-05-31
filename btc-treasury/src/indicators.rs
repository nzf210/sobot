//! Technical Indicators
//! Computes EMA, RSI, MACD, VWAP, ATR from OHLCV candles

use crate::models::Ohlcv;

pub struct Indicators;

impl Indicators {
    /// EMA with custom period
    pub fn ema(candles: &[Ohlcv], period: usize) -> Vec<f64> {
        if candles.len() < period || period == 0 {
            return vec![];
        }
        let k = 2.0 / (period as f64 + 1.0);
        let mut ema = Vec::with_capacity(candles.len());
        // First EMA = SMA of first `period` closes
        let init: f64 = candles[..period].iter().map(|c| c.close).sum::<f64>() / period as f64;
        ema.push(init);
        for i in period..candles.len() {
            let prev = ema[i - period];
            let curr = k * candles[i].close + (1.0 - k) * prev;
            ema.push(curr);
        }
        ema
    }

    /// Latest EMA(20)
    pub fn ema20(candles: &[Ohlcv]) -> f64 {
        let ema = Self::ema(candles, 20);
        ema.last().copied().unwrap_or(0.0)
    }

    /// Latest EMA(50)
    pub fn ema50(candles: &[Ohlcv]) -> f64 {
        let ema = Self::ema(candles, 50);
        ema.last().copied().unwrap_or(0.0)
    }

    /// Latest EMA(200)
    pub fn ema200(candles: &[Ohlcv]) -> f64 {
        let ema = Self::ema(candles, 200);
        ema.last().copied().unwrap_or(0.0)
    }

    /// RSI(14)
    pub fn rsi(candles:&[Ohlcv], period: usize) -> f64 {
        if candles.len() < period + 1 {
            return 50.0;
        }
        let mut gains = 0.0;
        let mut losses = 0.0;
        for i in 1..=period {
            let delta = candles[i].close - candles[i - 1].close;
            if delta > 0.0 {
                gains += delta;
            } else {
                losses += delta.abs();
            }
        }
        let avg_gain = gains / period as f64;
        let avg_loss = losses / period as f64;
        if avg_loss == 0.0 {
            return 100.0;
        }
        let rs = avg_gain / avg_loss;
        100.0 - (100.0 / (1.0 + rs))
    }

    /// EMA for f64 slices (used by MACD signal line)
    pub fn ema_f64(vals: &[f64], period: usize) -> f64 {
        if vals.len() < period || period == 0 {
            return 0.0;
        }
        let k = 2.0 / (period as f64 + 1.0);
        let init: f64 = vals[..period].iter().sum::<f64>() / period as f64;
        let mut ema = init;
        for i in period..vals.len() {
            ema = k * vals[i] + (1.0 - k) * ema;
        }
        ema
    }

    /// MACD(12, 26, 9) — returns (macd_line, signal_line, histogram)
    pub fn macd(candles:&[Ohlcv]) -> (f64, f64, f64) {
        let ema12 = Self::ema(candles, 12);
        let ema26 = Self::ema(candles, 26);
        if ema12.len() < 9 || ema26.len() < 9 {
            return (0.0, 0.0, 0.0);
        }
        let macd_line = ema12.last().unwrap() - ema26.last().unwrap();
        let macd_vals: Vec<f64> = ema12.iter()
            .zip(ema26.iter())
            .map(|(a, b)| a - b)
            .collect();
        let signal_line = Self::ema_f64(&macd_vals, 9);
        let histogram = macd_line - signal_line;
        (macd_line, signal_line, histogram)
    }

    /// VWAP (Volume Weighted Average Price) — latest candle
    pub fn vwap(candles: &[Ohlcv]) -> f64 {
        if candles.is_empty() {
            return 0.0;
        }
        let mut total_pv = 0.0;
        let mut total_vol = 0.0;
        for c in candles {
            let pv = c.close * c.volume;
            total_pv += pv;
            total_vol += c.volume;
        }
        if total_vol > 0.0 {
            total_pv / total_vol
        } else {
            0.0
        }
    }

    /// ATR(14) — Average True Range
    pub fn atr(candles:&[Ohlcv], period: usize) -> f64 {
        if candles.len() < period + 1 {
            return 0.0;
        }
        let mut trs = Vec::with_capacity(candles.len() - 1);
        for i in 1..candles.len() {
            let high = candles[i].high;
            let low = candles[i].low;
            let prev_close = candles[i - 1].close;
            let tr = (high - low).max((high - prev_close).abs()).max((low - prev_close).abs());
            trs.push(tr);
        }
        if trs.len() < period {
            return 0.0;
        }
        // First ATR = simple average of first `period` TRs
        let init: f64 = trs[..period].iter().sum::<f64>() / period as f64;
        let mut atr = init;
        let k = 1.0 / period as f64;
        for i in period..trs.len() {
            atr = atr * (1.0 - k) + trs[i] * k;
        }
        atr
    }

    /// Volume growth: (current_vol / avg_vol) - 1
    /// Positive = above average, negative = below
    pub fn volume_growth(candles:&[Ohlcv], lookback: usize) -> f64 {
        if candles.len() < lookback + 1 {
            return 0.0;
        }
        let recent =&candles[candles.len() - lookback - 1..candles.len() - 1];
        let avg_vol: f64 = recent.iter().map(|c| c.volume).sum::<f64>() / recent.len() as f64;
        let current_vol = candles.last().map(|c| c.volume).unwrap_or(0.0);
        if avg_vol > 0.0 {
            current_vol / avg_vol - 1.0
        } else {
            0.0
        }
    }

    /// Is volume expanding across timeframes? (1h vol > 4h avg, etc.)
    pub fn is_volume_expansion(candles_15m: &[Ohlcv], candles_1h: &[Ohlcv], candles_4h: &[Ohlcv]) -> bool {
        let avg_1h: f64 = if candles_1h.len() >= 4 {
            candles_1h.iter().rev().take(4).map(|c| c.volume).sum::<f64>() / 4.0
        } else {
            0.0
        };
        let avg_4h: f64 = if candles_4h.len() >= 4 {
            candles_4h.iter().rev().take(4).map(|c| c.volume).sum::<f64>() / 4.0
        } else {
            0.0
        };
        let vol_1h = candles_1h.last().map(|c| c.volume).unwrap_or(0.0);
        let vol_4h = candles_4h.last().map(|c| c.volume).unwrap_or(0.0);
        vol_1h > avg_1h && vol_4h > avg_4h
    }

    /// Return over a period (as fraction, not %)
    pub fn return_since(candles: &[Ohlcv], bars_back: usize) -> f64 {
        if candles.len() < bars_back + 1 {
            return 0.0;
        }
        let old = candles[candles.len() - bars_back - 1].close;
        let curr = candles.last().map(|c| c.close).unwrap_or(0.0);
        if old > 0.0 {
            (curr - old) / old
        } else {
            0.0
        }
    }
}
