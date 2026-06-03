#![allow(dead_code)]
use anyhow::Result;
use async_trait::async_trait;

use crate::models::{BtcAdvisoryPosition, BtcMarketData, Ohlcv};

#[derive(Debug, Clone)]
pub struct ExchangeBalance {
    pub asset: String,
    pub free: f64,
    pub locked: f64,
}

#[async_trait]
pub trait ExchangeClient: Send + Sync {
    /// Get all non-zero balances
    async fn get_balances(&self) -> Result<Vec<ExchangeBalance>>;

    /// Get market data for a symbol pair
    async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData>;

    /// Get open orders for a symbol
    async fn get_open_orders(&self, symbol: &str) -> Result<Vec<BtcAdvisoryPosition>>;

    /// Place a market buy order; returns order ID/status
    async fn place_market_buy(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult>;

    /// Place a market buy using quoteOrderQty — spend exactly `quote_amount`.
    async fn place_market_buy_quote(&self, symbol: &str, quote_amount: f64) -> Result<ExchangeOrderResult>;

    /// Place a limit buy order
    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult>;

    /// Place a market sell order; returns order ID/status
    async fn place_market_sell(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult>;

    /// Cancel a specific order
    async fn cancel_order(&self, symbol: &str, order_id: &str) -> Result<ExchangeOrderResult>;

    /// Cancel all open orders
    async fn cancel_all(&self, symbol: &str) -> Result<Vec<ExchangeOrderResult>>;

    /// Validate if a symbol is tradeable
    async fn validate_symbol(&self, symbol: &str) -> Result<bool>;

    /// Discover all BTC-quote pairs currently trading on this exchange.
    /// Used by `/btc_addpairs` to validate user input against the live pair
    /// universe in a single API call (instead of N `validate_symbol` calls,
    /// each of which fetches the full exchangeInfo endpoint).
    /// Default impl returns Err so non-Binance exchanges (OKX) get a clear
    /// "not supported" message without code changes.
    async fn discover_btc_pairs(&self) -> Result<Vec<String>> {
        Err(anyhow::anyhow!("discover_btc_pairs not implemented for this exchange"))
    }

    /// Get current price for a symbol (for position monitoring)
    async fn get_current_price(&self, symbol: &str) -> Result<f64>;

    /// Fetch OHLCV candles for technical analysis
    async fn get_klines(&self, symbol: &str, interval: &str, limit: u32) -> Result<Vec<Ohlcv>>;

    /// Human-readable exchange name
    fn exchange_name(&self) -> &'static str;

    /// Masked API key for display
    fn api_key_display(&self) -> String;
}

#[derive(Debug, Clone)]
pub struct ExchangeOrderResult {
    pub order_id: String,
    pub status: String,
    pub filled_qty: f64,
}

// ── Binance adapter ────────────────────────────────────────────────────────────

use crate::binance::BinanceClient;

#[async_trait]
impl ExchangeClient for BinanceClient {
    async fn get_balances(&self) -> Result<Vec<ExchangeBalance>> {
        let bals = self.get_balances().await?;
        Ok(bals
            .into_iter()
            .map(|b| {
                let free: f64 = b.free.parse().unwrap_or(0.0);
                let locked: f64 = b.locked.parse().unwrap_or(0.0);
                ExchangeBalance {
                    asset: b.asset,
                    free,
                    locked,
                }
            })
            .collect())
    }

    async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData> {
        self.get_market_data(symbol).await
    }

    async fn get_open_orders(&self, symbol: &str) -> Result<Vec<BtcAdvisoryPosition>> {
        let orders = self.get_open_orders(symbol).await?;
        Ok(orders
            .into_iter()
            .map(|o| BtcAdvisoryPosition {
                id: o.order_id.to_string(),
                entry_price: o.price.parse().unwrap_or(0.0),
                current_price: 0.0,
                size: o.orig_qty.parse().unwrap_or(0.0),
                pnl_btc: 0.0,
                entry_time: String::new(),
                side: o.side,
                take_profit_pct: 0.0,
                stop_loss_pct: 0.0,
                trailing_tp_pct: 0.0,
                use_trailing: false,
                llm_tp_reason: String::new(),
                llm_sl_reason: String::new(),
                llm_confidence: 0.0,
                highest_price: 0.0,
            })
            .collect())
    }

    async fn place_market_buy(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_order(symbol, "BUY", "MARKET", quantity, None).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn place_market_buy_quote(&self, symbol: &str, quote_amount: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_order_quote(symbol, quote_amount).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_order(symbol, "BUY", "LIMIT", quantity, Some(price)).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn place_market_sell(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_order(symbol, "SELL", "MARKET", quantity, None).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn cancel_order(&self, symbol: &str, order_id: &str) -> Result<ExchangeOrderResult> {
        let oid: i64 = order_id.parse()?;
        let res = self.cancel_order(symbol, oid).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn cancel_all(&self, symbol: &str) -> Result<Vec<ExchangeOrderResult>> {
        let results = self.cancel_all(symbol).await?;
        Ok(results
            .into_iter()
            .map(|r| ExchangeOrderResult {
                order_id: r.order_id.to_string(),
                status: r.status,
                filled_qty: 0.0,
            })
            .collect())
    }

    async fn validate_symbol(&self, symbol: &str) -> Result<bool> {
        self.validate_symbol(symbol).await
    }

    async fn discover_btc_pairs(&self) -> Result<Vec<String>> {
        Self::discover_btc_pairs(self).await
    }

    async fn get_current_price(&self, symbol: &str) -> Result<f64> {
        // Use orderbook mid price
        let l2 = self.get_orderbook(symbol).await?;
        let bids_arr = match l2["bids"].as_array() {
            Some(a) => a,
            None => return Err(anyhow::anyhow!("no bids for {}", symbol)),
        };
        let asks_arr = match l2["asks"].as_array() {
            Some(a) => a,
            None => return Err(anyhow::anyhow!("no asks for {}", symbol)),
        };

        let best_bid = bids_arr.first()
            .and_then(|l| l.as_array())
            .and_then(|l| l.get(0))
            .and_then(|v| v.as_str())
            .and_then(|s| s.parse::<f64>().ok())
            .unwrap_or(0.0);

        let best_ask = asks_arr.first()
            .and_then(|l| l.as_array())
            .and_then(|l| l.get(0))
            .and_then(|v| v.as_str())
            .and_then(|s| s.parse::<f64>().ok())
            .unwrap_or(0.0);

        if best_bid > 0.0 && best_ask > 0.0 {
            Ok((best_bid + best_ask) / 2.0)
        } else {
            Err(anyhow::anyhow!("no orderbook data for {}", symbol))
        }
    }

    fn exchange_name(&self) -> &'static str {
        "Binance"
    }

    async fn get_klines(&self, symbol: &str, interval: &str, limit: u32) -> Result<Vec<Ohlcv>> {
        self.get_klines(symbol, interval, limit).await
    }

    fn api_key_display(&self) -> String {
        self.api_key_display()
    }
}

// ── OKX adapter ──────────────────────────────────────────────────────────────

use crate::okx::OkxClient;

#[async_trait]
impl ExchangeClient for OkxClient {
    async fn get_balances(&self) -> Result<Vec<ExchangeBalance>> {
        self.get_balances().await
    }

    async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData> {
        self.get_market_data(symbol).await
    }

    async fn get_open_orders(&self, symbol: &str) -> Result<Vec<BtcAdvisoryPosition>> {
        self.get_open_orders(symbol).await
    }

    async fn place_market_buy(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult> {
        self.place_market_buy(symbol, quantity).await
    }

    async fn place_market_buy_quote(&self, symbol: &str, quote_amount: f64) -> Result<ExchangeOrderResult> {
        self.place_market_buy_quote(symbol, quote_amount).await
    }

    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult> {
        self.place_limit_buy(symbol, quantity, price).await
    }

    async fn place_market_sell(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult> {
        self.place_market_sell(symbol, quantity).await
    }

    async fn cancel_order(&self, symbol: &str, order_id: &str) -> Result<ExchangeOrderResult> {
        self.cancel_order(symbol, order_id).await
    }

    async fn cancel_all(&self, symbol: &str) -> Result<Vec<ExchangeOrderResult>> {
        self.cancel_all(symbol).await
    }

    async fn validate_symbol(&self, symbol: &str) -> Result<bool> {
        self.validate_symbol(symbol).await
    }

    async fn get_current_price(&self, symbol: &str) -> Result<f64> {
        self.get_current_price(symbol).await
    }

    fn exchange_name(&self) -> &'static str {
        "OKX"
    }

    async fn get_klines(&self, symbol: &str, interval: &str, limit: u32) -> Result<Vec<Ohlcv>> {
        self.get_klines(symbol, interval, limit).await
    }

    fn api_key_display(&self) -> String {
        self.api_key_display_pub()
    }
}

// Hyperliquid support removed – Binance Spot only.
// Any Hyperliquid-specific code has been stripped out.

