package manager

import (
	"go.uber.org/zap"

	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/models"
)

type MomentumLevel int

const (
	MomentumLow    MomentumLevel = iota
	MomentumMedium
	MomentumHigh
	MomentumExtreme
)

type MomentumAnalyzer struct {
	mem *memory.MemoryStore
	log *zap.Logger
}

func NewMomentumAnalyzer(mem *memory.MemoryStore, log *zap.Logger) *MomentumAnalyzer {
	return &MomentumAnalyzer{mem: mem, log: log}
}

type MomentumResult struct {
	Level        MomentumLevel
	Score        float64
	TrailPct     float64
	Reason       string
	ShouldTrail  bool
}

func (ma *MomentumAnalyzer) Analyze(pos models.Position, metric models.TokenMetrics) MomentumResult {
	result := MomentumResult{
		Level:       MomentumLow,
		Score:       0,
		TrailPct:    10.0,
		ShouldTrail: false,
	}

	currentPnL := ((metric.PriceSOL - pos.EntryPrice) / pos.EntryPrice) * 100.0

	if metric.Volume5m > 0 && metric.LiquidityUSD > 0 {
		volumeToLiquidityRatio := metric.Volume5m / metric.LiquidityUSD
		if volumeToLiquidityRatio > 2.0 {
			result.Score += 30
		} else if volumeToLiquidityRatio > 1.0 {
			result.Score += 20
		} else if volumeToLiquidityRatio > 0.5 {
			result.Score += 10
		}
	}

	if metric.OrganicScore > 70 {
		result.Score += 25
	} else if metric.OrganicScore > 50 {
		result.Score += 15
	}

	if currentPnL > 50 {
		result.Score += 30
	} else if currentPnL > 30 {
		result.Score += 20
	} else if currentPnL > 15 {
		result.Score += 10
	}

	if metric.MarketCapSOL > 0 {
		if metric.MarketCapSOL > 1000 {
			result.Score += 15
		} else if metric.MarketCapSOL > 500 {
			result.Score += 10
		}
	}

	if result.Score >= 70 {
		result.Level = MomentumExtreme
		result.TrailPct = 20.0
		result.ShouldTrail = true
		result.Reason = "Extreme momentum: volume spike + strong organic + high PnL"
	} else if result.Score >= 50 {
		result.Level = MomentumHigh
		result.TrailPct = 15.0
		result.ShouldTrail = true
		result.Reason = "High momentum: good volume and organic activity"
	} else if result.Score >= 30 {
		result.Level = MomentumMedium
		result.TrailPct = 12.0
		result.ShouldTrail = true
		result.Reason = "Medium momentum: moderate activity"
	} else {
		result.Level = MomentumLow
		result.TrailPct = 10.0
		result.ShouldTrail = false
		result.Reason = "Low momentum: use fixed TP"
	}

	return result
}
