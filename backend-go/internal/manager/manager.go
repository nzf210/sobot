package manager

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/executor"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
)

type Manager struct {
	mem *memory.MemoryStore
	log *zap.Logger
}

func New(mem *memory.MemoryStore, log *zap.Logger) *Manager {
	return &Manager{
		mem: mem,
		log: log,
	}
}

func (m *Manager) Start() {
	m.log.Info("Starting auto-sell Position Manager")
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

		pnlPct := ((metric.PriceSOL - pos.EntryPrice) / pos.EntryPrice) * 100.0

		shouldClose := false
		var closeReason string

		if pnlPct >= cfg.TakeProfitPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Take Profit hit at %.2f%%", pnlPct)
		} else if pnlPct <= cfg.StopLossPct {
			shouldClose = true
			closeReason = fmt.Sprintf("Stop Loss hit at %.2f%%", pnlPct)
		}

		if shouldClose {
			m.log.Info("Closing position", zap.String("token", pos.TokenAddress), zap.String("reason", closeReason))
			
			// We sell the whole amount we hold
			// In production, you'd get the actual token balance from RPC
			lamports := int64(pos.AmountToken * 1e6) // naive assumption 6 decimals for token, but usually it's dynamic.
			// The executor expects amount in base units for the inputMint. For tokens, we'd need decimals.
			// Let's pass the amount we got on entry as a placeholder.
			
			resp, err := executor.ExecuteSwap(pos.TokenAddress, "So11111111111111111111111111111111111111112", lamports)
			if err != nil || (resp != nil && !resp.Success) {
				m.log.Error("Failed to close position", zap.Error(err))
				continue
			}

			positions[i].IsClosed = true
			modified = true

			// Learning
			lesson := fmt.Sprintf("Trade on %s resulted in %.2f%% PNL. Reason: %s", pos.TokenAddress, pnlPct, closeReason)
			m.mem.AddLesson(lesson)
		}
	}

	if modified {
		m.mem.SavePositions(positions)
	}
}
