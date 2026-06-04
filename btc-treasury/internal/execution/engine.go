package execution

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"btc-treasury/internal/exchange"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/models"
	"btc-treasury/internal/monitor"
)

type ExecutionEngine struct {
	exchange exchange.ExchangeClient
	mem      *memory.MemoryStore
}

func NewExecutionEngine(ex exchange.ExchangeClient, mem *memory.MemoryStore) *ExecutionEngine {
	return &ExecutionEngine{
		exchange: ex,
		mem:      mem,
	}
}

func (ee *ExecutionEngine) ExecuteBuy(
	ctx context.Context,
	pair string,
	quoteAmount float64,
	advisory *models.FullBtcAdvisory,
) (models.ExecutionPlan, error) {
	if ee.exchange == nil {
		return models.ExecutionPlan{}, errors.New("exchange not configured")
	}

	price, err := ee.exchange.GetCurrentPrice(ctx, pair)
	if err != nil {
		return models.ExecutionPlan{}, fmt.Errorf("failed to get current price: %w", err)
	}

	result, err := ee.exchange.PlaceMarketBuyQuote(ctx, pair, quoteAmount)
	if err != nil {
		return models.ExecutionPlan{}, fmt.Errorf("failed to place market buy: %w", err)
	}

	quantity := 0.0
	if price > 0.0 {
		quantity = quoteAmount / price
	}

	log.Printf("BUY executed: %s quote=%.8f ~%.6f base — order_id=%s, status=%s",
		pair, quoteAmount, quantity, result.OrderID, result.Status)

	monitor.RecordPositionFromAdvisory(ee.mem, advisory, price, quantity, pair, "BUY")

	ee.mem.DeductBalanceForBuy(pair, quoteAmount)

	cfg := ee.mem.GetConfig()
	tpPrice := price * (1.0 + advisory.DynamicTakeProfit/100.0)
	slPrice := price * (1.0 + advisory.DynamicStopLoss/100.0)

	return models.ExecutionPlan{
		Action:           "BUY",
		Pair:             pair,
		Confidence:       advisory.Confidence,
		EntryPrice:       price,
		StopLossPrice:    slPrice,
		TakeProfitPrice:  tpPrice,
		PositionSizeUsdt: price * quantity,
		RiskPct:          cfg.RiskPerTradePct * 100.0,
		Reasons:          []string{advisory.Reason},
		TpPct:            advisory.DynamicTakeProfit,
		SlPct:            advisory.DynamicStopLoss,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (ee *ExecutionEngine) GetAvailableCapital(ctx context.Context, pair string) (float64, error) {
	if ee.exchange == nil {
		return 0.0, errors.New("exchange not configured")
	}

	balances, err := ee.exchange.GetBalances(ctx)
	if err != nil {
		return 0.0, fmt.Errorf("failed to get balances: %w", err)
	}

	pairUpper := strings.ToUpper(pair)
	isBtcQuote := strings.HasSuffix(pairUpper, "BTC") && pairUpper != "BTCUSDT"

	capital := 0.0
	if isBtcQuote {
		for _, b := range balances {
			if b.Asset == "BTC" {
				capital = b.Free
				break
			}
		}
	} else {
		for _, b := range balances {
			if b.Asset == "USDT" || b.Asset == "USDC" {
				capital = b.Free
				break
			}
		}
	}

	log.Printf("get_available_capital(%s): is_btc_quote=%t, capital=%.8f", pair, isBtcQuote, capital)
	return capital, nil
}

func (ee *ExecutionEngine) CancelAll(ctx context.Context, pair string) error {
	if ee.exchange == nil {
		return errors.New("exchange not configured")
	}

	_, err := ee.exchange.CancelAll(ctx, pair)
	return err
}
