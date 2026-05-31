use anyhow::Result;
use async_trait::async_trait;

use crate::models::{BtcAdvisoryPosition, BtcMarketData};

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

    /// Place a limit buy order
    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult>;

    /// Cancel a specific order
    async fn cancel_order(&self, symbol: &str, order_id: &str) -> Result<ExchangeOrderResult>;

    /// Cancel all open orders
    async fn cancel_all(&self, symbol: &str) -> Result<Vec<ExchangeOrderResult>>;

    /// Validate if a symbol is tradeable
    async fn validate_symbol(&self, symbol: &str) -> Result<bool>;

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

    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_order(symbol, "BUY", "LIMIT", quantity, Some(price)).await?;
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

    fn exchange_name(&self) -> &'static str {
        "Binance"
    }

    fn api_key_display(&self) -> String {
        self.api_key_display()
    }
}

// ── Hyperliquid adapter ─────────────────────────────────────────────────────────

use crate::hyperliquid::HyperliquidClient;

#[async_trait]
impl ExchangeClient for HyperliquidClient {
    async fn get_balances(&self) -> Result<Vec<ExchangeBalance>> {
        let bals = self.get_balances().await?;
        Ok(bals
            .into_iter()
            .map(|b| ExchangeBalance {
                asset: b.coin,
                free: b.total - b.hold,
                locked: b.hold,
            })
            .collect())
    }

    async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData> {
        self.get_market_data(symbol).await
    }

    async fn get_open_orders(&self, symbol: &str) -> Result<Vec<BtcAdvisoryPosition>> {
        let orders = self.get_open_orders().await?;
        Ok(orders
            .into_iter()
            .filter(|o| o.symbol == symbol)
            .map(|o| BtcAdvisoryPosition {
                id: o.oid.to_string(),
                entry_price: o.price.parse().unwrap_or(0.0),
                current_price: 0.0,
                size: o.sz.parse().unwrap_or(0.0),
                pnl_btc: 0.0,
                entry_time: String::new(),
                side: o.side,
            })
            .collect())
    }

    async fn place_market_buy(&self, symbol: &str, quantity: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_market_buy(symbol, quantity).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.map(|id| id.to_string()).unwrap_or_default(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn place_limit_buy(&self, symbol: &str, quantity: f64, price: f64) -> Result<ExchangeOrderResult> {
        let res = self.place_limit_buy(symbol, quantity, price).await?;
        Ok(ExchangeOrderResult {
            order_id: res.order_id.map(|id| id.to_string()).unwrap_or_default(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn cancel_order(&self, symbol: &str, order_id: &str) -> Result<ExchangeOrderResult> {
        let oid: i64 = order_id.parse()?;
        let res = self.cancel_order(symbol, oid).await?;
        Ok(ExchangeOrderResult {
            order_id: order_id.to_string(),
            status: res.status,
            filled_qty: 0.0,
        })
    }

    async fn cancel_all(&self, _symbol: &str) -> Result<Vec<ExchangeOrderResult>> {
        let results = self.cancel_all().await?;
        Ok(results
            .into_iter()
            .map(|r| ExchangeOrderResult {
                order_id: String::new(),
                status: r.status,
                filled_qty: 0.0,
            })
            .collect())
    }

    async fn validate_symbol(&self, symbol: &str) -> Result<bool> {
        self.validate_symbol(symbol).await
    }

    fn exchange_name(&self) -> &'static str {
        "Hyperliquid"
    }

    fn api_key_display(&self) -> String {
        self.api_key_display()
    }
}
