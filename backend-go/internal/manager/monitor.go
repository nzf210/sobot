package manager

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
	"hybrid-solana-bot/internal/models"
	"hybrid-solana-bot/internal/notifier"
)

type PositionStatus struct {
	Token      string
	EntryPrice float64
	CurrentPnL float64
	PeakPnL    float64
	TimeHeld   time.Duration
	Status     string // "profit", "floating_loss", "breakeven"
}

type PositionMonitor struct {
	cfg      config.Config
	mem      *memory.MemoryStore
	notifier *notifier.TelegramNotifier
	log      *zap.Logger
	interval time.Duration
}

func NewPositionMonitor(cfg config.Config, mem *memory.MemoryStore, notifier *notifier.TelegramNotifier, log *zap.Logger, intervalMinutes int) *PositionMonitor {
	return &PositionMonitor{
		cfg:      cfg,
		mem:      mem,
		notifier: notifier,
		log:      log,
		interval: time.Duration(intervalMinutes) * time.Minute,
	}
}

func (pm *PositionMonitor) Start() {
	pm.log.Info("Starting position monitor", zap.Duration("interval", pm.interval))
	ticker := time.NewTicker(pm.interval)
	defer ticker.Stop()

	for range ticker.C {
		pm.reportOpenPositions()
	}
}

func (pm *PositionMonitor) reportOpenPositions() {
	positions := pm.mem.GetPositions()
	openPositions := make([]models.Position, 0)

	for _, pos := range positions {
		if !pos.IsClosed {
			openPositions = append(openPositions, pos)
		}
	}

	if len(openPositions) == 0 {
		return
	}

	pm.log.Info("Position monitor report", zap.Int("open_positions", len(openPositions)))

	var msg string
	msg = fmt.Sprintf("📊 *Position Monitor (%d Open)*\n\n", len(openPositions))

	totalPnL := 0.0
	profitCount := 0
	lossCount := 0

	for i, pos := range openPositions {
		metric, err := metrics.FetchTokenMetrics(pos.TokenAddress)
		if err != nil {
			pm.log.Debug("Failed to fetch metrics for position", zap.String("token", pos.TokenAddress), zap.Error(err))
			continue
		}

		if metric.PriceSOL == 0 {
			continue
		}

		pnlPct := ((metric.PriceSOL - pos.EntryPrice) / pos.EntryPrice) * 100.0
		peakPnlPct := ((pos.HighestPrice - pos.EntryPrice) / pos.EntryPrice) * 100.0
		timeHeld := time.Since(pos.EntryTime)

		totalPnL += pnlPct

		status := "➖"
		if pnlPct > 5.0 {
			status = "📈"
			profitCount++
		} else if pnlPct < -5.0 {
			status = "📉"
			lossCount++
		}

		tokenShort := pos.TokenAddress
		if len(tokenShort) > 8 {
			tokenShort = tokenShort[:8] + "..."
		}

		msg += fmt.Sprintf("%d. %s `%s`\n", i+1, status, tokenShort)
		msg += fmt.Sprintf("   *PnL:* %.2f%% | *Peak:* %.2f%%\n", pnlPct, peakPnlPct)
		msg += fmt.Sprintf("   *Held:* %s\n", formatDuration(timeHeld))

		if timeHeld > 30*time.Minute {
			msg += fmt.Sprintf("   _⚠️ Held for %s_\n", formatDuration(timeHeld))
		}

		msg += "\n"
	}

	if len(openPositions) > 0 {
		msg += fmt.Sprintf("*Summary:* %d profit, %d loss, avg PnL %.2f%%\n",
			profitCount, lossCount, totalPnL/float64(len(openPositions)))
	}

	if err := pm.notifier.SendMessage(msg); err != nil {
		pm.log.Error("Failed to send position report", zap.Error(err))
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
