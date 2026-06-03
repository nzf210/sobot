use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use tokio::time::{interval, Duration};

use crate::engine::AdvisoryEngine;
use crate::exchange::ExchangeClient;
use crate::memory::MemoryStore;
use crate::models::*;

/// Monitors open positions and triggers TP/SL based on LLM-set or config defaults.
pub struct PositionMonitor {
    mem: Arc<MemoryStore>,
    exchange: Option<Arc<dyn ExchangeClient>>,
    engine: Arc<AdvisoryEngine>,
    /// Human-readable `exchange/account_id` label for log spans.
    label: String,
}

impl PositionMonitor {
    pub fn new(
        mem: Arc<MemoryStore>,
        exchange: Option<Arc<dyn ExchangeClient>>,
        engine: Arc<AdvisoryEngine>,
    ) -> Self {
        Self { mem, exchange, engine, label: String::new() }
    }

    /// Attach a `"exchange/account_id"` label used in log spans.
    pub fn with_label(mut self, label: impl Into<String>) -> Self {
        self.label = label.into();
        self
    }

    /// Start the monitoring loop — polls every 30 seconds.
    pub async fn start(self: Arc<Self>) {
        tracing::info!(label = %self.label, "BTC Position Monitor started");
        let mut tick = interval(Duration::from_secs(30));

        loop {
            tick.tick().await;
            self.check_positions().await;
        }
    }

    async fn check_positions(&self) {
        let Some(ref exchange) = self.exchange else {
            return;
        };

        let mut positions = self.mem.get_positions();
        if positions.is_empty() {
            return;
        }

        let cfg = self.mem.get_config();
        let mut modified = false;

        for i in 0..positions.len() {
            // Fetch current price
            let pair_id = positions[i].id.clone();
            let current_price = match exchange.get_current_price(&pair_id).await {
                Ok(p) => p,
                Err(e) => {
                    tracing::debug!("Failed to get price for {}: {}", pair_id, e);
                    continue;
                }
            };

            // Update highest price
            if current_price > positions[i].highest_price && current_price > 0.0 {
                positions[i].highest_price = current_price;
                modified = true;
            }

            positions[i].current_price = current_price;

            // Calculate PnL in BTC terms (percentage)
            let entry_price = positions[i].entry_price;
            let pnl_pct = if entry_price > 0.0 {
                ((current_price - entry_price) / entry_price) * 100.0
            } else {
                0.0
            };
            positions[i].pnl_btc = pnl_pct;

            // Resolve TP/SL: use position-specific (LLM) if set, else fall back to config
            let take_profit_pct = if positions[i].take_profit_pct > 0.0 {
                positions[i].take_profit_pct
            } else {
                cfg.take_profit_pct
            };

            let stop_loss_pct = if positions[i].stop_loss_pct != 0.0 {
                positions[i].stop_loss_pct
            } else {
                cfg.stop_loss_pct
            };

            let trail_pct = if positions[i].trailing_tp_pct > 0.0 {
                positions[i].trailing_tp_pct
            } else {
                cfg.trailing_tp_pct
            };

            let use_trailing = cfg.use_trailing || positions[i].use_trailing;
            let highest_price = positions[i].highest_price;
            let highest_pnl_pct = if entry_price > 0.0 && highest_price > 0.0 {
                ((highest_price - entry_price) / entry_price) * 100.0
            } else {
                0.0
            };

            let mut should_close = false;
            let mut close_reason = String::new();

            // Trailing TP check
            if use_trailing && highest_pnl_pct >= take_profit_pct {
                let drop_from_high_pct = if highest_price > 0.0 {
                    ((highest_price - current_price) / highest_price) * 100.0
                } else {
                    0.0
                };

                if drop_from_high_pct >= trail_pct {
                    should_close = true;
                    close_reason = format!(
                        "Trailing Stop hit: dropped {:.1}% from peak (Peak PnL: {:.2}%, Trail: {:.0}%, TP: {:.1}%, SL: {:.1}%)",
                        drop_from_high_pct, highest_pnl_pct, trail_pct, take_profit_pct, stop_loss_pct
                    );
                }
            } else if !use_trailing && pnl_pct >= take_profit_pct {
                // Simple TP check
                should_close = true;
                close_reason = format!("Take Profit hit at {:.2}% (target: {:.1}%)", pnl_pct, take_profit_pct);
            } else if pnl_pct <= stop_loss_pct {
                // SL check
                should_close = true;
                close_reason = format!("Stop Loss hit at {:.2}% (limit: {:.1}%)", pnl_pct, stop_loss_pct);
            }

            if should_close {
                let cfg = self.mem.get_config();
                tracing::info!("Closing BTC position {}: {}", pair_id, close_reason);
                modified = true;

                let position_size = positions[i].size;
                let entry = positions[i].entry_price;

                if cfg.dry_run {
                    // Dry run: simulate close without calling exchange
                    let position_value = entry * position_size;
                    // For BTC-quote pairs (SOLBTC): btc_price=1.0 signals PnL already in BTC
                    let btc_price_for_conversion = if pair_id.to_uppercase().ends_with("BTC") && pair_id.to_uppercase() != "BTCUSDT" {
                        1.0
                    } else {
                        current_price
                    };
                    self.mem.update_treasury_on_close(&pair_id, pnl_pct, position_value, btc_price_for_conversion);
                } else {
                    // Live: execute market sell to close the position
                    let close_result = exchange.place_market_sell(&pair_id, position_size).await;
                    match close_result {
                        Ok(result) => {
                            tracing::info!(
                                "Position {} closed via market sell: order_id={}, status={}",
                                pair_id, result.order_id, result.status
                            );
                            // Update BTC treasury with realized PnL
                            let position_value = entry * position_size;
                            let btc_price_for_conversion = if pair_id.to_uppercase().ends_with("BTC") && pair_id.to_uppercase() != "BTCUSDT" {
                                1.0
                            } else {
                                current_price
                            };
                            self.mem.update_treasury_on_close(&pair_id, pnl_pct, position_value, btc_price_for_conversion);
                            // Re-sync the local ledger with the live Binance
                            // balances now that the close has filled. The
                            // PnL-based update above is the rough estimate;
                            // the live balances are the source of truth for
                            // the next risk calculation.
                            if let Ok(balances) = exchange.get_balances().await {
                                let live_btc: f64 = balances.iter()
                                    .find(|b| b.asset == "BTC")
                                    .map(|b| b.free + b.locked)
                                    .unwrap_or(0.0);
                                let live_usdt: f64 = balances.iter()
                                    .find(|b| b.asset == "USDT" || b.asset == "USDC")
                                    .map(|b| b.free + b.locked)
                                    .unwrap_or(0.0);
                                self.mem.resync_after_fill(live_btc, live_usdt);
                            }
                        }
                        Err(e) => {
                            tracing::error!("Failed to execute market sell for {}: {}", pair_id, e);
                            continue;
                        }
                    }
                }

                // Log the close
                let quality = if pnl_pct > 10.0 {
                    "excellent"
                } else if pnl_pct > 0.0 {
                    "good"
                } else if pnl_pct > -5.0 {
                    "neutral"
                } else {
                    "bad"
                };

                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap()
                    .as_millis() as i64;
                let ts = chrono::DateTime::from_timestamp_millis(now)
                    .map(|dt| dt.format("%Y-%m-%d %H:%M").to_string())
                    .unwrap_or_default();

                let lesson = format!(
                    "[BTC][{}] {}: PnL {:.2}% (peak {:.2}%). Entry: {:.2}, Exit: {:.2}. Quality: {}. Close: {}. TP: {:.1}%, SL: {:.1}%",
                    ts,
                    pair_id,
                    pnl_pct,
                    highest_pnl_pct,
                    entry_price,
                    current_price,
                    quality,
                    close_reason,
                    take_profit_pct,
                    stop_loss_pct
                );
                self.mem.add_lesson(lesson);

                // Auto-pause on consecutive losses
                {
                    let mut treasury = self.mem.get_treasury_state();
                    if pnl_pct <= 0.0 {
                        treasury.consecutive_losses += 1;
                        treasury.losing_trades += 1;
                        let cfg = self.mem.get_config();
                        if treasury.consecutive_losses >= cfg.max_consecutive_losses {
                            let pause_until = chrono::Utc::now() + chrono::Duration::hours(24);
                            treasury.trading_paused_until = pause_until.to_rfc3339();
                            tracing::warn!(
                                "BTC AUTO-PAUSE: {} consecutive losses — trading paused until {}",
                                treasury.consecutive_losses, pause_until.format("%Y-%m-%d %H:%M UTC")
                            );
                        }
                    } else {
                        treasury.winning_trades += 1;
                        treasury.consecutive_losses = 0; // reset loss streak on win
                    }
                    self.mem.save_treasury_state(treasury);
                }

                // Remove from positions
                positions.remove(i);
                break;
            }
        }

        if modified {
            self.mem.save_positions(&positions);
        }
    }
}

/// Record a new position from an APPROVE advisory with dynamic TP/SL.
pub fn record_position_from_advisory(
    mem: &MemoryStore,
    advisory: &FullBtcAdvisory,
    entry_price: f64,
    size: f64,
    pair: &str,
    side: &str,
) {
    let cfg = mem.get_config();

    let position = BtcAdvisoryPosition {
        id: pair.to_string(),
        entry_price,
        current_price: entry_price,
        size,
        pnl_btc: 0.0,
        entry_time: chrono::Utc::now().to_rfc3339(),
        side: side.to_string(),
        // Dynamic TP/SL from LLM advisory
        take_profit_pct: if advisory.dynamic_take_profit > 0.0 {
            advisory.dynamic_take_profit
        } else {
            cfg.take_profit_pct
        },
        stop_loss_pct: if advisory.dynamic_stop_loss != 0.0 {
            advisory.dynamic_stop_loss
        } else {
            cfg.stop_loss_pct
        },
        trailing_tp_pct: cfg.trailing_tp_pct,
        use_trailing: cfg.use_trailing,
        llm_tp_reason: advisory.tp_reason.clone(),
        llm_sl_reason: advisory.sl_reason.clone(),
        llm_confidence: advisory.confidence,
        highest_price: entry_price,
    };

    let mut positions = mem.get_positions();
    positions.push(position);
    mem.save_positions(&positions);

    tracing::info!(
        "Recorded BTC position {}: TP={:.1}%, SL={:.1}% (LLM reason: {})",
        pair,
        advisory.dynamic_take_profit,
        advisory.dynamic_stop_loss,
        advisory.tp_reason
    );
}
