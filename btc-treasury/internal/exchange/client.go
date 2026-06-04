package exchange

import (
	"context"

	"btc-treasury/internal/models"
)

type ExchangeClient interface {
	// GetBalances returns all non-zero balances
	GetBalances(ctx context.Context) ([]models.ExchangeBalance, error)

	// GetMarketData returns market data for a symbol pair
	GetMarketData(ctx context.Context, symbol string) (models.BtcMarketData, error)

	// GetOpenOrders returns open orders for a symbol
	GetOpenOrders(ctx context.Context, symbol string) ([]models.BtcAdvisoryPosition, error)

	// PlaceMarketBuy places a market buy order; returns order ID/status
	PlaceMarketBuy(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error)

	// PlaceMarketBuyQuote places a market buy using quoteOrderQty — spend exactly quoteAmount
	PlaceMarketBuyQuote(ctx context.Context, symbol string, quoteAmount float64) (models.ExchangeOrderResult, error)

	// PlaceLimitBuy places a limit buy order
	PlaceLimitBuy(ctx context.Context, symbol string, quantity float64, price float64) (models.ExchangeOrderResult, error)

	// PlaceMarketSell places a market sell order; returns order ID/status
	PlaceMarketSell(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error)

	// CancelOrder cancels a specific order
	CancelOrder(ctx context.Context, symbol string, orderID string) (models.ExchangeOrderResult, error)

	// CancelAll cancels all open orders
	CancelAll(ctx context.Context, symbol string) ([]models.ExchangeOrderResult, error)

	// ValidateSymbol validates if a symbol is tradeable
	ValidateSymbol(ctx context.Context, symbol string) (bool, error)

	// DiscoverBtcPairs discovers all BTC-quote pairs currently trading on this exchange
	DiscoverBtcPairs(ctx context.Context) ([]string, error)

	// GetCurrentPrice gets current price for a symbol (for position monitoring)
	GetCurrentPrice(ctx context.Context, symbol string) (float64, error)

	// GetKlines fetches OHLCV candles for technical analysis
	GetKlines(ctx context.Context, symbol string, interval string, limit uint32) ([]models.Ohlcv, error)

	// ExchangeName returns a human-readable exchange name
	ExchangeName() string

	// APIKeyDisplay returns a masked API key for display
	APIKeyDisplay() string
}
