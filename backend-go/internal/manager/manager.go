package manager

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/executor"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
	"hybrid-solana-bot/internal/notifier"
)

type Manager struct {
	cfg      config.Config
	mem      *memory.MemoryStore
	log      *zap.Logger
	notifier *notifier.TelegramNotifier
	monitor  *PositionMonitor
	momentum *MomentumAnalyzer
}

func New(cfg config.Config, mem *memory.MemoryStore, log *zap.Logger) *Manager {
	tgNotifier := notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs)
	return &Manager{
		cfg:      cfg,
		mem:      mem,
		log:      log,
		notifier: tgNotifier,
		monitor:  NewPositionMonitor(cfg, mem, tgNotifier, log, 5),
		momentum: NewMomentumAnalyzer(mem, log),
	}
}

func (m *Manager) Start() {
	m.log.Info("Starting auto-sell Position Manager")
	go m.monitor.Start()
	for {
		m.checkPositions()
		time.Sleep(10 * time.Second) // poll every 10 seconds
	}
}

func (m *Manager) checkPositions() {
	cfg := m.mem.GetUserConfig()
	if !cfg.AutoTrade {
		return
	}

	positions := m.mem.GetPositions()
	modified := false

	for i, pos := range positions {
		if pos.IsClosed {
			continue
		}

		// fetch current metrics to check price
		metric, err := metrics.FetchTokenMetrics(pos.TokenAddress)
		if err != nil {
			m.log.Debug("Manager failed to fetch token metrics", zap.Error(err), zap.String("token", pos.TokenAddress))
			continue
		}

		if metric.PriceSOL == 0 {
			continue
		}

		if metric.PriceSOL > pos.HighestPrice {
			positions[i].HighestPrice = metric.PriceSOL
			modified = true
			pos.HighestPrice = metric.PriceSOL
		}

		pnlPct := ((metric.PriceSOL - pos.EntryPrice) / pos.EntryPrice) * 100.0
		highestPnlPct := ((pos.HighestPrice - pos.EntryPrice) / pos.EntryPrice) * 100.0

		// Resolve TP/SL: use position-specific (LLM) if set, else fall back to config
		takeProfitPct := cfg.TakeProfitPct
		stopLossPct := cfg.StopLossPct
		if pos.TakeProfitPct > 0 {
			takeProfitPct = pos.TakeProfitPct
		}
		if pos.StopLossPct != 0 {
			stopLossPct = pos.StopLossPct
		}

		// Trailing TP: use position-specific if set, else momentum-based
		trailPct := 10.0
		if pos.TrailingTPPct > 0 {
			trailPct = pos.TrailingTPPct
		}

		// Analyze momentum for smart trailing
		momentumResult := m.momentum.Analyze(pos, metric)
		shouldClose := false
		var closeReason string

		// Smart trailing: auto-activate if high momentum or position uses trailing
		useTrailing := (cfg.TrailingTakeProfit || pos.UseTrailing) || momentumResult.ShouldTrail
		if momentumResult.ShouldTrail && pos.TrailingTPPct > 0 {
			trailPct = momentumResult.TrailPct
			m.log.Info("Smart trailing activated (overriding momentum)",
				zap.String("token", pos.TokenAddress),
				zap.Float64("trail_pct", trailPct),
				zap.Int("momentum_level", int(momentumResult.Level)),
				zap.String("reason", momentumResult.Reason))
		}

		if useTrailing && highestPnlPct >= takeProfitPct {
			dropFromHighPct := ((pos.HighestPrice - metric.PriceSOL) / pos.HighestPrice) * 100.0
			if dropFromHighPct >= trailPct {
				shouldClose = true
				if pos.UseTrailing || momentumResult.ShouldTrail {
					closeReason = fmt.Sprintf("Smart Trailing Stop: dropped %.1f%% from peak (Peak PnL: %.2f%%, Trail: %.0f%%, TP: %.1f%%, SL: %.1f%%, Momentum: %s)",
						dropFromHighPct, highestPnlPct, trailPct, takeProfitPct, stopLossPct, momentumResult.Reason)
				} else {
					closeReason = fmt.Sprintf("Trailing Stop hit: dropped %.1f%% from peak (Peak PnL was %.2f%%, TP: %.1f%%)", dropFromHighPct, highestPnlPct, takeProfitPct)
				}
			}
		} else if !useTrailing && pnlPct >= takeProfitPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Take Profit hit at %.2f%% (target: %.1f%%)", pnlPct, takeProfitPct)
		} else if pnlPct <= stopLossPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Stop Loss hit at %.2f%% (limit: %.1f%%)", pnlPct, stopLossPct)
		}

		if shouldClose {
			m.log.Info("Closing position", zap.String("token", pos.TokenAddress), zap.String("reason", closeReason))

			if cfg.DryRun {
				// DRY RUN: simulate close, mark as closed without real swap
				positions[i].IsClosed = true
				positions[i].ExitPrice = metric.PriceSOL
				positions[i].ExitTime = time.Now()
				positions[i].ProfitLossUsd = pnlPct
				positions[i].LowestPrice = metric.PriceSOL
				modified = true

				quality := "bad"
				if pnlPct > 10.0 {
					quality = "excellent"
				} else if pnlPct > 0 {
					quality = "good"
				} else if pnlPct > -5.0 {
					quality = "neutral"
				}

				lesson := fmt.Sprintf("[DRY RUN][%s] Token %s: PnL %.2f%% (peak %.2f%%), held %s. Entry: %.8f SOL, Exit: %.8f SOL. Decision quality: %s. Close reason: %s. TP: %.1f%%, SL: %.1f%%",
					time.Now().Format("2006-01-02 15:04"), pos.TokenAddress[:8]+"...", pnlPct, highestPnlPct,
					formatDuration(time.Since(pos.EntryTime)), pos.EntryPrice, metric.PriceSOL, quality, closeReason, takeProfitPct, stopLossPct)
				m.mem.AddLesson(lesson)

				msg := fmt.Sprintf("🧪 *[DRY RUN] Simulasi SELL*\n*Token:* `%s`\n*PnL Sim:* %.2f%% (peak: %.2f%%)\n*Held:* %s\n*Quality:* %s\n*Reason:* %s\n⚠️ _Tidak ada transaksi nyata._",
					pos.TokenAddress[:8]+"...", pnlPct, highestPnlPct, formatDuration(time.Since(pos.EntryTime)), quality, closeReason)
				m.notifier.SendMessage(msg)
				continue
			}

			lamports := int64(pos.AmountToken * 1e6)
			resp, err := executor.ExecuteSwap(pos.TokenAddress, "So11111111111111111111111111111111111111112", lamports)
			if err != nil {
				m.log.Error("Failed to close position (executor call failed)", zap.Error(err), zap.String("token", pos.TokenAddress))
				continue
			}
			if resp == nil || !resp.Success {
				respErr := "unknown error"
				if resp != nil && resp.Result.Error != "" {
					respErr = resp.Result.Error
				}
				m.log.Error("Failed to close position (swap returned failure)", zap.String("error", respErr), zap.String("token", pos.TokenAddress))
				continue
			}

			positions[i].IsClosed = true
			positions[i].ExitPrice = metric.PriceSOL
			positions[i].ExitTime = time.Now()
			positions[i].ProfitLossUsd = pnlPct
			positions[i].LowestPrice = metric.PriceSOL
			modified = true

			quality := "bad"
			if pnlPct > 10.0 {
				quality = "excellent"
			} else if pnlPct > 0 {
				quality = "good"
			} else if pnlPct > -5.0 {
				quality = "neutral"
			}

			lesson := fmt.Sprintf("[%s] Token %s: PnL %.2f%% (peak %.2f%%), held %s. Entry: %.8f SOL, Exit: %.8f SOL. Decision quality: %s. Close reason: %s",
				time.Now().Format("2006-01-02 15:04"), pos.TokenAddress[:8]+"...", pnlPct, highestPnlPct,
				formatDuration(time.Since(pos.EntryTime)), pos.EntryPrice, metric.PriceSOL, quality, closeReason)
			m.mem.AddLesson(lesson)

			msg := fmt.Sprintf("🚨 *Auto-Sell Triggered!*\n*Token:* `%s`\n*PnL:* %.2f%% (peak: %.2f%%)\n*Held:* %s\n*Quality:* %s\n*Reason:* %s",
				pos.TokenAddress[:8]+"...", pnlPct, highestPnlPct, formatDuration(time.Since(pos.EntryTime)), quality, closeReason)
			m.notifier.SendMessage(msg)
		}
	}

	if modified {
		m.mem.SavePositions(positions)
	}
}
