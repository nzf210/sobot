package indicators

import (
	"math"

	"btc-treasury/internal/models"
)

// EMA calculates exponential moving average for a slice of Ohlcv candles.
func EMA(candles []models.Ohlcv, period int) []float64 {
	if len(candles) < period || period <= 0 {
		return nil
	}
	k := 2.0 / (float64(period) + 1.0)
	ema := make([]float64, 0, len(candles)-period+1)

	// First EMA is the SMA of the first `period` closes
	var sum float64
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	initVal := sum / float64(period)
	ema = append(ema, initVal)

	for i := period; i < len(candles); i++ {
		prev := ema[len(ema)-1]
		curr := k*candles[i].Close + (1.0-k)*prev
		ema = append(ema, curr)
	}
	return ema
}

func EMA20(candles []models.Ohlcv) float64 {
	vals := EMA(candles, 20)
	if len(vals) == 0 {
		return 0.0
	}
	return vals[len(vals)-1]
}

func EMA50(candles []models.Ohlcv) float64 {
	vals := EMA(candles, 50)
	if len(vals) == 0 {
		return 0.0
	}
	return vals[len(vals)-1]
}

func EMA200(candles []models.Ohlcv) float64 {
	vals := EMA(candles, 200)
	if len(vals) == 0 {
		return 0.0
	}
	return vals[len(vals)-1]
}

// RSI calculates Wilder's smoothed Relative Strength Index.
func RSI(candles []models.Ohlcv, period int) float64 {
	if len(candles) < period+1 || period <= 0 {
		return 50.0
	}

	// Seed: SMA of first `period` gain/loss values.
	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		delta := candles[i].Close - candles[i-1].Close
		if delta > 0 {
			avgGain += delta
		} else {
			avgLoss += math.Abs(delta)
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Wilder smooth: alpha = 1 / period
	for i := period + 1; i < len(candles); i++ {
		delta := candles[i].Close - candles[i-1].Close
		var g, l float64
		if delta >= 0 {
			g = delta
		} else {
			l = math.Abs(delta)
		}
		avgGain = (avgGain*float64(period-1) + g) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + l) / float64(period)
	}

	if avgLoss == 0.0 {
		return 100.0
	}
	rs := avgGain / avgLoss
	return 100.0 - (100.0 / (1.0 + rs))
}

// EMAF64 calculates EMA for raw float64 values (used by MACD signal line).
func EMAF64(vals []float64, period int) float64 {
	if len(vals) < period || period <= 0 {
		return 0.0
	}
	k := 2.0 / (float64(period) + 1.0)
	var sum float64
	for i := 0; i < period; i++ {
		sum += vals[i]
	}
	ema := sum / float64(period)

	for i := period; i < len(vals); i++ {
		ema = k*vals[i] + (1.0-k)*ema
	}
	return ema
}

// MACD calculates MACD line, signal line, and histogram.
func MACD(candles []models.Ohlcv) (float64, float64, float64) {
	ema12 := EMA(candles, 12)
	ema26 := EMA(candles, 26)
	if len(ema12) < 9 || len(ema26) < 9 {
		return 0.0, 0.0, 0.0
	}

	macdLine := ema12[len(ema12)-1] - ema26[len(ema26)-1]

	macdVals := make([]float64, len(ema26))
	for i := 0; i < len(ema26); i++ {
		macdVals[i] = ema12[i] - ema26[i]
	}

	signalLine := EMAF64(macdVals, 9)
	histogram := macdLine - signalLine
	return macdLine, signalLine, histogram
}

// VWAP calculates Volume Weighted Average Price for the candles.
func VWAP(candles []models.Ohlcv) float64 {
	if len(candles) == 0 {
		return 0.0
	}
	var totalPV, totalVol float64
	for _, c := range candles {
		pv := c.Close * c.Volume
		totalPV += pv
		totalVol += c.Volume
	}
	if totalVol > 0.0 {
		return totalPV / totalVol
	}
	return 0.0
}

// ATR calculates Average True Range.
func ATR(candles []models.Ohlcv, period int) float64 {
	if len(candles) < period+1 || period <= 0 {
		return 0.0
	}
	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		high := candles[i].High
		low := candles[i].Low
		prevClose := candles[i-1].Close
		tr := math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
		trs = append(trs, tr)
	}

	if len(trs) < period {
		return 0.0
	}

	// First ATR = average of first `period` TRs
	var sum float64
	for i := 0; i < period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	k := 1.0 / float64(period)
	for i := period; i < len(trs); i++ {
		atr = atr*(1.0-k) + trs[i]*k
	}
	return atr
}

func ReturnSince(candles []models.Ohlcv, barsBack int) float64 {
	if len(candles) < barsBack+1 {
		return 0.0
	}
	old := candles[len(candles)-barsBack-1].Close
	curr := candles[len(candles)-1].Close
	if old > 0.0 {
		return (curr - old) / old
	}
	return 0.0
}

func VolumeGrowth(candles []models.Ohlcv, lookback int) float64 {
	if len(candles) < lookback+1 {
		return 0.0
	}
	recent := candles[len(candles)-lookback-1 : len(candles)-1]
	var sum float64
	for _, c := range recent {
		sum += c.Volume
	}
	avgVol := sum / float64(len(recent))
	currentVol := candles[len(candles)-1].Volume
	if avgVol > 0.0 {
		return currentVol/avgVol - 1.0
	}
	return 0.0
}

func IsVolumeExpansion(candles15m, candles1h, candles4h []models.Ohlcv) bool {
	var avg1h float64
	if len(candles1h) >= 4 {
		var sum float64
		for i := 0; i < 4; i++ {
			sum += candles1h[len(candles1h)-1-i].Volume
		}
		avg1h = sum / 4.0
	}

	var avg4h float64
	if len(candles4h) >= 4 {
		var sum float64
		for i := 0; i < 4; i++ {
			sum += candles4h[len(candles4h)-1-i].Volume
		}
		avg4h = sum / 4.0
	}

	var vol1h float64
	if len(candles1h) > 0 {
		vol1h = candles1h[len(candles1h)-1].Volume
	}

	var vol4h float64
	if len(candles4h) > 0 {
		vol4h = candles4h[len(candles4h)-1].Volume
	}

	return vol1h > avg1h && vol4h > avg4h
}
