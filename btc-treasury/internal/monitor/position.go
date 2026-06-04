package monitor

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"btc-treasury/internal/exchange"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/models"
)

type StatusTracker interface {
	IsEnabled() bool
	Touch()
}

type PositionMonitor struct {
	mem      *memory.MemoryStore
	exchange exchange.ExchangeClient
	label    string
	status   StatusTracker
}

func NewPositionMonitor(mem *memory.MemoryStore, ex exchange.ExchangeClient, status StatusTracker) *PositionMonitor {
	return &PositionMonitor{
		mem:      mem,
		exchange: ex,
		status:   status,
	}
}

func (pm *PositionMonitor) WithLabel(label string) *PositionMonitor {
	pm.label = label
	return pm
}

func (pm *PositionMonitor) Start(ctx context.Context) {
	log.Printf("[%s] BTC Position Monitor started", pm.label)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] BTC Position Monitor stopping", pm.label)
			return
		case <-ticker.C:
			if pm.status != nil {
				if !pm.status.IsEnabled() {
					log.Printf("[%s] Position monitor is disabled/paused, skipping tick", pm.label)
					continue
				}
			}
			pm.CheckPositions(ctx)
		}
	}
}

func (pm *PositionMonitor) CheckPositions(ctx context.Context) {
	if pm.exchange == nil {
		return
	}

	positions := pm.mem.GetPositions()
	if len(positions) == 0 {
		return
	}

	cfg := pm.mem.GetConfig()
	modified := false
	var positionsToRemove []int

	for i := 0; i < len(positions); i++ {
		pairID := positions[i].ID
		currentPrice, err := pm.exchange.GetCurrentPrice(ctx, pairID)
		if err != nil {
			log.Printf("[%s] Failed to get price for %s: %v", pm.label, pairID, err)
			continue
		}

		if currentPrice > positions[i].HighestPrice && currentPrice > 0.0 {
			positions[i].HighestPrice = currentPrice
			modified = true
		}

		positions[i].CurrentPrice = currentPrice

		entryPrice := positions[i].EntryPrice
		var pnlPct float64
		if entryPrice > 0.0 {
			pnlPct = ((currentPrice - entryPrice) / entryPrice) * 100.0
		}
		positions[i].PnlBtc = pnlPct

		takeProfitPct := cfg.TakeProfitPct
		if positions[i].TakeProfitPct > 0.0 {
			takeProfitPct = positions[i].TakeProfitPct
		}

		stopLossPct := cfg.StopLossPct
		if positions[i].StopLossPct != 0.0 {
			stopLossPct = positions[i].StopLossPct
		}

		trailPct := cfg.TrailingTpPct
		if positions[i].TrailingTpPct > 0.0 {
			trailPct = positions[i].TrailingTpPct
		}

		useTrailing := cfg.UseTrailing || positions[i].UseTrailing
		highestPrice := positions[i].HighestPrice
		var highestPnlPct float64
		if entryPrice > 0.0 && highestPrice > 0.0 {
			highestPnlPct = ((highestPrice - entryPrice) / entryPrice) * 100.0
		}

		shouldClose := false
		closeReason := ""

		if useTrailing && highestPnlPct >= takeProfitPct {
			var dropFromHighPct float64
			if highestPrice > 0.0 {
				dropFromHighPct = ((highestPrice - currentPrice) / highestPrice) * 100.0
			}
			if dropFromHighPct >= trailPct {
				shouldClose = true
				closeReason = fmt.Sprintf(
					"Trailing Stop hit: dropped %.1f%% from peak (Peak PnL: %.2f%%, Trail: %.0f%%, TP: %.1f%%, SL: %.1f%%)",
					dropFromHighPct, highestPnlPct, trailPct, takeProfitPct, stopLossPct,
				)
			}
		} else if !useTrailing && pnlPct >= takeProfitPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Take Profit hit at %.2f%% (target: %.1f%%)", pnlPct, takeProfitPct)
		} else if pnlPct <= stopLossPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Stop Loss hit at %.2f%% (limit: %.1f%%)", pnlPct, stopLossPct)
		}

		if shouldClose {
			log.Printf("[%s] Closing BTC position %s: %s", pm.label, pairID, closeReason)
			modified = true
			positionSize := positions[i].Size
			entry := positions[i].EntryPrice

			if cfg.DryRun {
				positionValue := entry * positionSize
				btcPriceForConversion := currentPrice
				if strings.HasSuffix(strings.ToUpper(pairID), "BTC") && strings.ToUpper(pairID) != "BTCUSDT" {
					btcPriceForConversion = 1.0
				}

				if !pm.mem.UpdateTreasuryOnClose(pairID, pnlPct, positionValue, btcPriceForConversion) {
					log.Printf("[%s] Treasury update refused for %s — keeping position open, will retry next tick", pm.label, pairID)
					modified = false
					continue
				}
			} else {
				_, err := pm.exchange.PlaceMarketSell(ctx, pairID, positionSize)
				if err != nil {
					log.Printf("[%s] Failed to execute market sell for %s: %v", pm.label, pairID, err)
					modified = false
					continue
				}

				log.Printf("[%s] Position %s closed via market sell", pm.label, pairID)

				positionValue := entry * positionSize
				btcPriceForConversion := currentPrice
				if strings.HasSuffix(strings.ToUpper(pairID), "BTC") && strings.ToUpper(pairID) != "BTCUSDT" {
					btcPriceForConversion = 1.0
				}

				if !pm.mem.UpdateTreasuryOnClose(pairID, pnlPct, positionValue, btcPriceForConversion) {
					log.Printf("[%s] Treasury update refused for %s (order filled) — resyncing ledger anyway", pm.label, pairID)
				}

				balances, err := pm.exchange.GetBalances(ctx)
				if err == nil {
					var liveBtc, liveUsdt float64
					for _, b := range balances {
						if b.Asset == "BTC" {
							liveBtc = b.Free + b.Locked
						} else if b.Asset == "USDT" || b.Asset == "USDC" {
							liveUsdt = b.Free + b.Locked
						}
					}
					pm.mem.ResyncAfterFill(liveBtc, liveUsdt)
				}
			}

			quality := "neutral"
			if pnlPct > 5.0 {
				quality = "excellent"
			} else if pnlPct > 0.0 {
				quality = "good"
			} else if pnlPct > -2.0 {
				quality = "neutral"
			} else {
				quality = "bad"
			}

			ts := time.Now().UTC().Format("2006-01-02 15:04")
			lesson := fmt.Sprintf(
				"[BTC][%s] %s: PnL %.2f%% (peak %.2f%%). Entry: %.6f, Exit: %.6f. Quality: %s. Close: %s. TP: %.1f%%, SL: %.1f%%",
				ts, pairID, pnlPct, highestPnlPct, entryPrice, currentPrice, quality, closeReason, takeProfitPct, stopLossPct,
			)
			pm.mem.AddLesson(lesson)

			// Update consecutive losses and auto-pause
			{
				treasury := pm.mem.GetTreasuryState()
				if pnlPct <= 0.0 {
					treasury.ConsecutiveLosses++
					if treasury.ConsecutiveLosses >= cfg.MaxConsecutiveLosses {
						pauseUntil := time.Now().Add(24 * time.Hour)
						treasury.TradingPausedUntil = pauseUntil.Format(time.RFC3339)
						log.Printf("[%s] BTC AUTO-PAUSE: %d consecutive losses — trading paused until %s",
							pm.label, treasury.ConsecutiveLosses, pauseUntil.Format("2006-01-02 15:04 UTC"))
					}
				} else {
					treasury.ConsecutiveLosses = 0
				}
				pm.mem.SaveTreasuryState(treasury)
			}

			positionsToRemove = append(positionsToRemove, i)
		}
	}

	// Remove closed positions in reverse order
	for idx := len(positionsToRemove) - 1; idx >= 0; idx-- {
		removeIdx := positionsToRemove[idx]
		positions = append(positions[:removeIdx], positions[removeIdx+1:]...)
	}

	if modified || len(positionsToRemove) > 0 {
		pm.mem.SavePositions(positions)
	}
}

func RecordPositionFromAdvisory(
	mem *memory.MemoryStore,
	advisory *models.FullBtcAdvisory,
	entryPrice float64,
	size float64,
	pair string,
	side string,
) {
	cfg := mem.GetConfig()

	takeProfitPct := cfg.TakeProfitPct
	if advisory.DynamicTakeProfit > 0.0 {
		takeProfitPct = advisory.DynamicTakeProfit
	}

	stopLossPct := cfg.StopLossPct
	if advisory.DynamicStopLoss != 0.0 {
		stopLossPct = advisory.DynamicStopLoss
	}

	position := models.BtcAdvisoryPosition{
		ID:             pair,
		EntryPrice:     entryPrice,
		CurrentPrice:   entryPrice,
		Size:           size,
		PnlBtc:         0.0,
		EntryTime:      time.Now().UTC().Format(time.RFC3339),
		Side:           side,
		TakeProfitPct:  takeProfitPct,
		StopLossPct:    stopLossPct,
		TrailingTpPct:  cfg.TrailingTpPct,
		UseTrailing:    cfg.UseTrailing,
		LlmTpReason:    advisory.TpReason,
		LlmSlReason:    advisory.SlReason,
		LlmConfidence:  advisory.Confidence,
		HighestPrice:   entryPrice,
	}

	positions := mem.GetPositions()
	positions = append(positions, position)
	mem.SavePositions(positions)

	log.Printf("Recorded BTC position %s: TP=%.1f%%, SL=%.1f%% (LLM reason: %s)",
		pair, advisory.DynamicTakeProfit, advisory.DynamicStopLoss, advisory.TpReason)
}
