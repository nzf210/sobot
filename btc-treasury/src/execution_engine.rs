#![allow(dead_code)]
//! Execution Engine
//! Executes market buy/sell, records positions, manages treasury split on close.

use std::sync::Arc;

use crate::exchange::ExchangeClient;
use crate::memory::MemoryStore;
use crate::models::*;
use crate::position_monitor::record_position_from_advisory;

pub struct ExecutionEngine {
    exchange: Option<Arc<dyn ExchangeClient>>,
    mem: Arc<MemoryStore>,
}

impl ExecutionEngine {
    pub fn new(exchange: Option<Arc<dyn ExchangeClient>>, mem: Arc<MemoryStore>) -> Self {
        Self { exchange, mem }
    }

    /// Execute a market SELL to close a position. Returns treasury update.
    ///
    /// **DELETED**: This method was never called from production paths.
    /// The actual close flow runs through `position_monitor::check_positions`
    /// which calls `place_market_sell` directly and then
    /// `MemoryStore::update_treasury_on_close` for BTC accounting.
    /// Keeping this here would risk a future caller re-introducing a
    /// double-counted profit split (this method also applied the
    /// 50/50 vault/compound split, which the position-monitor path
    /// already does via `cfg.treasury_pct` / `cfg.compound_pct`).

    /// Compute treasury update (BTC accounting JSON)
    /// `quote_amount` is the amount of QUOTE currency to spend (USDT for
    /// BTCUSDT, BTC for SOLBTC). Position `size` recorded downstream is the
    /// derived BASE quantity (e.g. BTC, SOL) so the close path
    /// (`place_market_sell(pair, base_qty)`) sells what we actually hold.
    pub async fn execute_buy(
        &self,
        pair: &str,
        quote_amount: f64,
        advisory: &FullBtcAdvisory,
    ) -> anyhow::Result<ExecutionPlan> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;

        let price = exchange.get_current_price(pair).await?;
        let result = exchange.place_market_buy_quote(pair, quote_amount).await?;

        // Estimate base quantity for position recording
        let quantity = if price > 0.0 { quote_amount / price } else { 0.0 };

        tracing::info!(
            "BUY executed: {} quote={:.8} ~{:.6} base — order_id={}, status={}",
            pair, quote_amount, quantity, result.order_id, result.status
        );

        // Record position with LLM-set TP/SL
        record_position_from_advisory(
            &self.mem,
            advisory,
            price,
            quantity,
            pair,
            "BUY",
        );

        // Deduct spent quote from the local ledger so subsequent risk
        // calcs see the post-buy balance. Without this, the ledger drifts
        // from Binance on every buy, eventually causing oversized
        // positions or live-rejection on insufficient balance.
        self.mem.deduct_balance_for_buy(pair, quote_amount);

        let cfg = self.mem.get_config();
        let tp_price = price * (1.0 + advisory.dynamic_take_profit / 100.0);
        let sl_price = price * (1.0 + advisory.dynamic_stop_loss / 100.0);

        Ok(ExecutionPlan {
            action: "BUY".to_string(),
            pair: pair.to_string(),
            confidence: advisory.confidence,
            entry_price: price,
            stop_loss_price: sl_price,
            take_profit_price: tp_price,
            position_size_usdt: price * quantity,
            risk_pct: cfg.risk_per_trade_pct * 100.0,
            reasons: vec![advisory.reason.clone()],
            tp_pct: advisory.dynamic_take_profit,
            sl_pct: advisory.dynamic_stop_loss,
            timestamp: chrono::Utc::now().to_rfc3339(),
        })
    }

    /// Get available capital for position sizing, based on the pair's quote currency.
    /// For BTC-quote pairs (SOLBTC, ETHBTC): returns free BTC balance.
    /// For USDT/USDC-quote pairs (BTCUSDT): returns free USDT/USDC balance.
    pub async fn get_available_capital(&self, pair: &str) -> anyhow::Result<f64> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;
        let balances = exchange.get_balances().await?;

        let is_btc_quote = pair.to_uppercase().ends_with("BTC") && pair.to_uppercase() != "BTCUSDT";

        let capital = if is_btc_quote {
            balances.iter()
                .find(|b| b.asset == "BTC")
                .map(|b| b.free)
                .unwrap_or(0.0)
        } else {
            balances.iter()
                .find(|b| b.asset == "USDT" || b.asset == "USDC")
                .map(|b| b.free)
                .unwrap_or(0.0)
        };

        tracing::debug!(
            "get_available_capital({}): is_btc_quote={}, capital={:.8}",
            pair, is_btc_quote, capital
        );

        Ok(capital)
    }

    /// Cancel all open orders for a pair
    pub async fn cancel_all(&self, pair: &str) -> anyhow::Result<()> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;
        exchange.cancel_all(pair).await?;
        Ok(())
    }
}
