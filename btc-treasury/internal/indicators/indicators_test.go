package indicators

import (
	"math"
	"testing"

	"btc-treasury/internal/models"
)

func TestEMA(t *testing.T) {
	candles := []models.Ohlcv{
		{Close: 10.0},
		{Close: 11.0},
		{Close: 12.0},
		{Close: 13.0},
		{Close: 14.0},
	}
	// EMA with period 3
	ema := EMA(candles, 3)
	if len(ema) != 3 {
		t.Fatalf("expected len 3, got %d", len(ema))
	}
	// First EMA is SMA(3) of first 3 closes = (10+11+12)/3 = 11.0
	if math.Abs(ema[0]-11.0) > 0.0001 {
		t.Errorf("expected ema[0] = 11.0, got %f", ema[0])
	}
	// Next EMA = (13 * 0.5) + (11.0 * 0.5) = 12.0 (since k = 2/(3+1) = 0.5)
	if math.Abs(ema[1]-12.0) > 0.0001 {
		t.Errorf("expected ema[1] = 12.0, got %f", ema[1])
	}
}

func TestRSI(t *testing.T) {
	candles := make([]models.Ohlcv, 20)
	for i := 0; i < 20; i++ {
		candles[i] = models.Ohlcv{Close: 100.0}
	}
	rsi := RSI(candles, 14)
	if math.Abs(rsi-100.0) > 0.0001 {
		t.Errorf("expected flat RSI = 100.0, got %f", rsi)
	}
}
