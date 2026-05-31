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

    /// Execute a market BUY for a pair. Returns the execution result.
    pub async fn execute_buy(
        &self,
        pair: &str,
        quantity: f64,
        advisory: &FullBtcAdvisory,
    ) -> anyhow::Result<ExecutionPlan> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;

        let price = exchange.get_current_price(pair).await?;
        let result = exchange.place_market_buy(pair, quantity).await?;

        tracing::info!(
            "BUY executed: {} {} at ~{} — order_id={}, status={}",
            pair, quantity, price, result.order_id, result.status
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

    /// Execute a market SELL to close a position. Returns treasury update.
    pub async fn execute_sell(
&self,
        pair: &str,
        quantity: f64,
        entry_price: f64,
    ) -> anyhow::Result<TreasuryUpdate> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;

        let current_price = exchange.get_current_price(pair).await?;
        let result = exchange.place_market_sell(pair, quantity).await?;

        tracing::info!(
            "SELL executed: {} {} at ~{} — order_id={}, status={}",
            pair, quantity, current_price, result.order_id, result.status
        );

        // Calculate PnL
        let pnl_pct = if entry_price > 0.0 {
            ((current_price - entry_price) / entry_price) * 100.0
        } else {
            0.0
        };

        let position_value_usdt = entry_price * quantity;
        let pnl_usdt = position_value_usdt * (pnl_pct / 100.0);

        // BTC accounting
        let treasury_before = self.mem.get_treasury_state().current_btc;
        let update = self.compute_treasury_update(
            pair,
            treasury_before,
            pnl_pct,
            pnl_usdt,
            current_price,
            "market_sell".to_string(),
        );

        // Apply50/50 compound/treasury split + update trade stats
        self.apply_treasury_split(pnl_usdt);

        Ok(update)
    }

    /// Compute treasury update (BTC accounting JSON)
    fn compute_treasury_update(
        &self,
        pair: &str,
        btc_before: f64,
        pnl_pct: f64,
        pnl_usdt: f64,
        current_price: f64,
        close_reason: String,
    ) -> TreasuryUpdate {
        let btc_price = current_price; // quote is USDT
        let profit_btc = if pnl_usdt > 0.0 {
            pnl_usdt / btc_price
        } else {
            pnl_usdt / btc_price
        };

        let compound_btc = profit_btc * 0.50;
        let treasury_btc = profit_btc * 0.50;
        let btc_after = btc_before + treasury_btc;
        let btc_gain = btc_after - btc_before;

        TreasuryUpdate {
            pair: pair.to_string(),
            btc_before,
            btc_after,
            btc_gain,
            profit_btc,
            compound_btc,
            treasury_btc,
            close_reason,
            pnl_pct,
            timestamp: chrono::Utc::now().to_rfc3339(),
        }
    }

    /// After a winning close, split profit: 50% compound (re-enter capital), 50% treasury vault.
    fn apply_treasury_split(&self, pnl_usdt: f64) {
        if pnl_usdt <= 0.0 {
            return;
        }
        let mut state = self.mem.get_treasury_state();
        let btc_price = 65_000.0; // TODO: fetch real BTCUSDT price
        let profit_btc = pnl_usdt / btc_price;
        let treasury_delta = profit_btc * 0.50;
        let compound_delta = profit_btc * 0.50;

        state.btc_treasury_vault += treasury_delta;
        state.compound_balance += compound_delta;
        state.total_trades += 1;
        state.winning_trades += 1;

        self.mem.save_treasury_state(state);
        tracing::info!(
            "Treasury split: +{:.8} BTC to vault, +{:.8} BTC compound",
            treasury_delta, compound_delta
        );
    }

    /// Get available capital (USDT balance) for position sizing
    pub async fn get_available_capital(&self) -> anyhow::Result<f64> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;
        let balances = exchange.get_balances().await?;
        let usdt = balances
            .iter()
            .find(|b| b.asset == "USDT" || b.asset == "USDC")
            .map(|b| b.free)
            .unwrap_or(0.0);
        Ok(usdt)
    }

    /// Cancel all open orders for a pair
    pub async fn cancel_all(&self, pair: &str) -> anyhow::Result<()> {
        let exchange = self.exchange.as_ref()
            .ok_or_else(|| anyhow::anyhow!("exchange not configured"))?;
        exchange.cancel_all(pair).await?;
        Ok(())
    }
}
