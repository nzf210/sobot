#![allow(dead_code)]
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use tokio::sync::RwLock;
use tokio::time::{interval, Duration};

use crate::engine::AdvisoryEngine;
use crate::engines::ai_scoring::AIScoringEngine;
use crate::engines::risk_manager::RiskManager;
use crate::engines::volume_engine::VolumeEngine;
use crate::execution_engine::ExecutionEngine;
use crate::exchange::{ExchangeClient};
use crate::indicators::Indicators;
use crate::memory::MemoryStore;
use crate::models::*;
use crate::position_monitor::record_position_from_advisory;

/// Returns true if pair is a BTC-quote pair (SOLBTC, ETHBTC), not BTCUSDT.
fn is_btc_quote_pair(pair: &str) -> bool {
    pair.to_uppercase().ends_with("BTC") && pair.to_uppercase() != "BTCUSDT"
}

#[derive(Debug, Clone)]
pub struct RecentDecision {
    pub pair: String,
    pub timestamp: String,
    pub recommendation: String,
    pub confidence: f64,
    pub risk_level: String,
    pub reason: String,
}

pub struct ScannerStats {
    pub scanned: AtomicU64,
    pub advisory_approve: AtomicU64,
    pub advisory_monitor: AtomicU64,
    pub advisory_protect: AtomicU64,
    pub advisory_reject: AtomicU64,
    pub errors: AtomicU64,
}

impl ScannerStats {
    pub fn new() -> Self {
        Self {
            scanned: AtomicU64::new(0),
            advisory_approve: AtomicU64::new(0),
            advisory_monitor: AtomicU64::new(0),
            advisory_protect: AtomicU64::new(0),
            advisory_reject: AtomicU64::new(0),
            errors: AtomicU64::new(0),
        }
    }

    pub fn snapshot(&self) -> ScannerStatsSnapshot {
        ScannerStatsSnapshot {
            scanned: self.scanned.load(Ordering::Relaxed),
            approve: self.advisory_approve.load(Ordering::Relaxed),
            monitor: self.advisory_monitor.load(Ordering::Relaxed),
            protect: self.advisory_protect.load(Ordering::Relaxed),
            reject: self.advisory_reject.load(Ordering::Relaxed),
            errors: self.errors.load(Ordering::Relaxed),
        }
    }
}

#[derive(Debug, Clone)]
pub struct ScannerStatsSnapshot {
    pub scanned: u64,
    pub approve: u64,
    pub monitor: u64,
    pub protect: u64,
    pub reject: u64,
    pub errors: u64,
}

pub struct PairState {
    pub stats: ScannerStats,
    pub last_scan_time: RwLock<String>,
    pub last_regime: RwLock<String>,
    pub last_recommendation: RwLock<String>,
    pub last_confidence: RwLock<f64>,
    pub last_risk_level: RwLock<String>,
    pub last_reason: RwLock<String>,
}

impl PairState {
    pub fn new() -> Self {
        Self {
            stats: ScannerStats::new(),
            last_scan_time: RwLock::new(String::new()),
            last_regime: RwLock::new(String::new()),
            last_recommendation: RwLock::new(String::new()),
            last_confidence: RwLock::new(0.0),
            last_risk_level: RwLock::new(String::new()),
            last_reason: RwLock::new(String::new()),
        }
    }
}

#[derive(Debug, Clone)]
pub struct PairSnapshot {
    pub pair: String,
    pub stats: ScannerStatsSnapshot,
    pub last_scan_time: String,
    pub last_regime: String,
    pub last_recommendation: String,
    pub last_confidence: f64,
    pub last_risk_level: String,
    pub last_reason: String,
}

pub struct ScannerState {
    pub pairs: RwLock<HashMap<String, Arc<PairState>>>,
    pub pair_list: RwLock<Vec<String>>,
    pub recent_decisions: RwLock<Vec<RecentDecision>>,
}

impl ScannerState {
    pub fn new() -> Self {
        Self {
            pairs: RwLock::new(HashMap::new()),
            pair_list: RwLock::new(Vec::new()),
            recent_decisions: RwLock::new(Vec::new()),
        }
    }

    pub async fn initialize_pairs(&self, pairs: &[String]) {
        let mut map = self.pairs.write().await;
        let mut list = self.pair_list.write().await;
        for pair in pairs {
            let name = pair.trim().to_uppercase();
            if name.is_empty() || map.contains_key(&name) {
                continue;
            }
            map.insert(name.clone(), Arc::new(PairState::new()));
            list.push(name);
        }
    }

    pub async fn add_pair(&self, pair: &str) -> bool {
        let name = pair.trim().to_uppercase();
        if name.is_empty() {
            return false;
        }
        let mut map = self.pairs.write().await;
        let mut list = self.pair_list.write().await;
        if map.contains_key(&name) {
            return false;
        }
        map.insert(name.clone(), Arc::new(PairState::new()));
        list.push(name.clone());
        tracing::info!("Scanner: added pair {}", name);
        true
    }

    pub async fn remove_pair(&self, pair: &str) -> bool {
        let name = pair.trim().to_uppercase();
        let mut map = self.pairs.write().await;
        let mut list = self.pair_list.write().await;
        if map.remove(&name).is_some() {
            list.retain(|p| p != &name);
            tracing::info!("Scanner: removed pair {}", name);
            true
        } else {
            false
        }
    }

    pub async fn get_pairs(&self) -> Vec<String> {
        self.pair_list.read().await.clone()
    }

    pub async fn get_pair_state(&self, pair: &str) -> Option<Arc<PairState>> {
        self.pairs.read().await.get(pair).cloned()
    }

    pub async fn all_snapshots(&self) -> Vec<PairSnapshot> {
        let pairs = self.pairs.read().await;
        let mut snapshots: Vec<PairSnapshot> = Vec::new();
        for (name, ps) in pairs.iter() {
            snapshots.push(PairSnapshot {
                pair: name.clone(),
                stats: ps.stats.snapshot(),
                last_scan_time: ps.last_scan_time.read().await.clone(),
                last_regime: ps.last_regime.read().await.clone(),
                last_recommendation: ps.last_recommendation.read().await.clone(),
                last_confidence: *ps.last_confidence.read().await,
                last_risk_level: ps.last_risk_level.read().await.clone(),
                last_reason: ps.last_reason.read().await.clone(),
            });
        }
        snapshots.sort_by(|a, b| a.pair.cmp(&b.pair));
        snapshots
    }
}

pub async fn run(
    state: Arc<ScannerState>,
    exchange: Arc<dyn ExchangeClient>,
    engine: Arc<AdvisoryEngine>,
    executor: Arc<ExecutionEngine>,
    mem: Arc<MemoryStore>,
    interval_secs: u64,
    status: Arc<crate::account_runtime::AccountStatus>,
) {
    let mut tick = interval(Duration::from_secs(interval_secs));
    let exname = exchange.exchange_name().to_string();
    tracing::info!(
        exchange = %exname,
        "Multi-pair scanner started (every {}s)", interval_secs
    );

    loop {
        tick.tick().await;
        if !status.is_enabled() {
            status.touch();
            tracing::debug!(exchange = %exname, "Scanner is disabled/paused, skipping tick");
            continue;
        }
        // Touch heartbeat — supervisor + /btc/accounts can see this runtime is alive.
        status.touch();

        let pairs = state.get_pairs().await;
        if pairs.is_empty() {
            tracing::warn!(exchange = %exname, "Scanner: no pairs configured");
            continue;
        }

        for pair in &pairs {
            if let Some(ps) = state.get_pair_state(pair).await {
                scan_pair(&state, pair, &ps, &*exchange, &engine, &executor, &mem).await;
                tokio::time::sleep(Duration::from_millis(500)).await;
            }
        }
    }
}

async fn scan_pair(
    state: &ScannerState,
    pair: &str,
    ps: &PairState,
    exchange: &dyn ExchangeClient,
    engine: &AdvisoryEngine,
    executor: &ExecutionEngine,
    mem: &MemoryStore,
) {
    ps.stats.scanned.fetch_add(1, Ordering::Relaxed);

    let now = chrono::Utc::now().to_rfc3339();
    *ps.last_scan_time.write().await = now.clone();

    let market_data = match exchange.get_market_data(pair).await {
        Ok(data) => data,
        Err(e) => {
            ps.stats.errors.fetch_add(1, Ordering::Relaxed);
            tracing::error!("Scanner [{}]: failed to fetch market data: {}", pair, e);
            return;
        }
    };

    let open_orders = exchange.get_open_orders(pair).await.ok().unwrap_or_default();

    let treasury = mem.get_treasury_state();

    // Check trading pause
    if !treasury.trading_paused_until.is_empty() {
        if let Ok(paused) = chrono::DateTime::parse_from_rfc3339(&treasury.trading_paused_until) {
            if chrono::Utc::now() < paused {
                tracing::debug!("Scanner [{}]: skipping (trading paused until {})", pair, paused);
                return;
            }
        }
    }

    let config = mem.get_config();
    if config.dry_run {
        tracing::debug!("Scanner [{}]: dry_run mode active", pair);
    }

    let _stored_positions = mem.get_positions();
    let loss_streak = treasury.consecutive_losses;

    // Fetch OHLCV and compute AI technical scoring for better advisory
    let (ai_score, risk_info, pair_metrics) = match exchange.get_klines(pair, "15m", 200).await {
        Ok(candles_15m) if candles_15m.len() > 50 => {
            // Fetch longer timeframe candles
            let candles_1h = exchange.get_klines(pair, "1h", 50).await.unwrap_or_default();
            let candles_4h = exchange.get_klines(pair, "4h", 50).await.unwrap_or_default();
            let btc_15m = exchange.get_klines("BTCUSDT", "15m", 200).await.unwrap_or_default();

            let mut metrics = compute_pair_metrics(&candles_15m, &candles_1h, &candles_4h, &btc_15m, pair);
            // Populate live orderbook metrics from BtcMarketData
            metrics.spread_pct = (10.0 - market_data.spread_score) / 20.0;
            metrics.wide_spread = metrics.spread_pct >= 0.5;
            let min_depth = market_data.liquidity_score * 50.0;
            metrics.bid_depth = min_depth;
            metrics.ask_depth = min_depth;
            metrics.liquidity_growth = metrics.volume_growth > 0.0;
            metrics.wash_trade_detected = VolumeEngine::is_wash_trade(&metrics);

            let risk_assessment = RiskManager::assess(
                &treasury,
                mem.get_positions().len() as i32,
                loss_streak,
                // `drawdown_pct` is a ratio (0.05 = 5%), not a USD value.
                // The previous `usdt_balance * btc_growth_7d` form was
                // (USD × ratio) which under-weighted any reasonable growth
                // and starved the risk engine of the real drawdown signal.
                // We approximate drawdown from the absolute BTC growth ratio:
                // a 5% drop is drawdown=0.05.
                treasury.btc_growth_7d.abs().min(1.0),
                treasury.usdt_balance,
                &config,
            );
            let scoring = AIScoringEngine::score_pair(&metrics, &risk_assessment);
            (Some(scoring.score), Some(risk_assessment), Some(metrics))
        }
        _ => {
            tracing::debug!("Scanner [{}]: insufficient OHLCV data, using orderbook-only scoring", pair);
            (None, None, None)
        }
    };

    let input = BtcAdvisoryInput {
        market_data: market_data.clone(),
        treasury: treasury.clone(),
        open_positions: open_orders,
        loss_streak,
        ai_score,
        risk_assessment: risk_info,
        pair_metrics: pair_metrics.clone(),
    };

    let mut advisory = engine.analyze(&input).await;

    // Confidence + score gate: the LLM/quant path can return APPROVE with
    // middling scores (e.g. CONFIDENCE 0.6, score 65). User-configured
    // `min_confidence` and `min_score_threshold` are the real floor — block
    // execution when below them. Without this, a noisy regime can trigger
    // low-quality trades that drag BTC down.
    let min_conf = config.min_confidence;
    let min_score = config.min_score_threshold;
    if advisory.recommendation == "APPROVE"
        && (advisory.confidence < min_conf || advisory.opportunity_score < min_score)
    {
        tracing::info!(
            "Scanner [{}]: APPROVE blocked — conf {:.2} < {} OR score {:.0} < {}",
            pair, advisory.confidence, min_conf, advisory.opportunity_score, min_score
        );
        advisory.recommendation = "MONITOR".to_string();
        advisory.reason = format!(
            "{} (blocked: conf {:.2} < {:.2} or score {:.0} < {:.0})",
            advisory.reason, advisory.confidence, min_conf, advisory.opportunity_score, min_score
        );
    }

    *ps.last_regime.write().await = advisory.market_regime.clone();
    *ps.last_recommendation.write().await = advisory.recommendation.clone();
    *ps.last_reason.write().await = advisory.reason.clone();
    *ps.last_confidence.write().await = advisory.confidence;
    *ps.last_risk_level.write().await = advisory.risk_level.clone();

    match advisory.recommendation.as_str() {
        "APPROVE" => {
            ps.stats.advisory_approve.fetch_add(1, Ordering::Relaxed);

            // Execute the approved trade
            let cfg = mem.get_config();
            let positions = mem.get_positions();

            // Check if we can open a new position
            let can_trade = positions.len() < cfg.max_positions as usize
                && treasury.trading_paused_until.is_empty();

            if can_trade {
                let capital = match executor.get_available_capital(pair).await {
                    Ok(c) => c,
                    Err(e) => {
                        tracing::error!("Scanner [{}]: failed to get capital: {}", pair, e);
                        0.0
                    }
                };

                let current_price = exchange.get_current_price(pair).await.unwrap_or(0.0);
                let quote_asset = if is_btc_quote_pair(pair) { "BTC" } else { "USDT" };

                // Clamp SL: use ATR-based minimum width to prevent SL from being too tight
                let clamped_sl = if let Some(ref pm) = pair_metrics {
                    let close = if pm.close_15m > 0.0 { pm.close_15m } else { current_price };
                    RiskManager::clamp_sl(advisory.dynamic_stop_loss, close, pm.atr_14)
                } else {
                    RiskManager::min_sl_from_atr(current_price, 0.0) // floor only (0.8%)
                };
                if clamped_sl != advisory.dynamic_stop_loss {
                    tracing::info!(
                        "Scanner [{}]: clamped SL from {:.1}% to {:.1}% (ATR_14={:.6}, close={:.6})",
                        pair, advisory.dynamic_stop_loss, clamped_sl, pair_metrics.as_ref().map(|pm| pm.atr_14).unwrap_or(0.0), current_price
                    );
                    // Also widen TP proportionally to keep risk/reward >= 2:1
                    let tp_sl_ratio = if advisory.dynamic_stop_loss != 0.0 {
                        (advisory.dynamic_take_profit / advisory.dynamic_stop_loss.abs()).max(2.0)
                    } else {
                        3.0
                    };
                    advisory.dynamic_stop_loss = clamped_sl;
                    advisory.dynamic_take_profit = clamped_sl.abs() * tp_sl_ratio;
                    advisory.tp_reason = format!("{} (wider SL due to ATR clamp)", advisory.tp_reason);
                    advisory.sl_reason = format!("{} (ATR clamp: {:.1}% min)", advisory.sl_reason, -clamped_sl);
                }

                let position_value = if capital > 0.0 && advisory.dynamic_stop_loss < 0.0 {
                    RiskManager::calc_position_size(
                        capital,
                        current_price,
                        advisory.dynamic_stop_loss,
                        cfg.risk_per_trade_pct,
                        cfg.taker_fee_pct,
                    )
                } else {
                    0.0
                };

                if position_value > 0.0 {
                    if cfg.dry_run {
                        // Estimate base qty so position_monitor can simulate close.
                        // For BTC-quote pairs (SOLBTC): base = quote(BTC) / price(BTC/SOL) = SOL.
                        // For USDT-quote pairs (BTCUSDT): base = quote(USDT) / price(USDT/BTC) = BTC.
                        let sim_size = if current_price > 0.0 { position_value / current_price } else { 0.0 };
                        record_position_from_advisory(mem, &advisory, current_price, sim_size, pair, "BUY");
                        tracing::info!(
                            "[DRY RUN] Scanner [{}]: APPROVE — simulated BUY of {:.2} {} (≈{:.8} base) at score {:.0}",
                            pair, position_value, quote_asset, sim_size, advisory.opportunity_score
                        );
                    } else {
                        match executor.execute_buy(pair, position_value, &advisory).await {
                            Ok(plan) => {
                                let qty = if plan.entry_price > 0.0 { position_value / plan.entry_price } else { 0.0 };
                                tracing::info!(
                                    "Scanner [{}]: BUY executed — {} {:.8} (value {:.2} {}) (TP:{:.1}%, SL:{:.1}%)",
                                    pair, plan.pair, qty, position_value, quote_asset, plan.tp_pct, plan.sl_pct
                                );
                            }
                            Err(e) => {
                                tracing::error!("Scanner [{}]: BUY execution failed: {}", pair, e);
                            }
                        }
                    }
                } else {
                    tracing::warn!("Scanner [{}]: APPROVE but zero position_value computed (capital={:.2} {})", pair, capital, quote_asset);
                }
            } else {
                tracing::debug!(
                    "Scanner [{}]: APPROVE blocked — positions={}/{} paused={}",
                    pair, positions.len(), cfg.max_positions, !treasury.trading_paused_until.is_empty()
                );
            }
        }
        "MONITOR" => {
            ps.stats.advisory_monitor.fetch_add(1, Ordering::Relaxed);
        }
        "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => {
            ps.stats.advisory_protect.fetch_add(1, Ordering::Relaxed);
        }
        _ => {
            ps.stats.advisory_reject.fetch_add(1, Ordering::Relaxed);
        }
    }

    // Push to recent decisions ring buffer
    let decision = RecentDecision {
        pair: pair.to_string(),
        timestamp: now,
        recommendation: advisory.recommendation.clone(),
        confidence: advisory.confidence,
        risk_level: advisory.risk_level.clone(),
        reason: advisory.reason.clone(),
    };
    {
        let mut recents = state.recent_decisions.write().await;
        recents.push(decision);
        if recents.len() > 50 {
            recents.remove(0);
        }
    }

    // Log to persistent decision log
    let record = BtcDecisionRecord {
        timestamp: advisory.timestamp.clone(),
        market_data,
        treasury_before: treasury,
        treasury_after: mem.get_treasury_state(),
        advisory: advisory.clone(),
        action_taken: advisory.recommendation.clone(),
    };
    mem.log_decision(record);

    // Generate lesson for non-APPROVE recommendations
    if advisory.recommendation != "APPROVE" {
        let lesson = format!(
            "[{}] [{}] advisory: {} (regime: {}, confidence: {:.2}, risk: {}) — {}",
            chrono::Utc::now().format("%Y-%m-%d %H:%M"),
            pair,
            advisory.recommendation,
            advisory.market_regime,
            advisory.confidence,
            advisory.risk_level,
            advisory.reason
        );
        mem.add_lesson(lesson);
    }
}

/// Compute PairMetrics from OHLCV candles for AI scoring engines
fn compute_pair_metrics(
    candles_15m: &[Ohlcv],
    candles_1h: &[Ohlcv],
    candles_4h: &[Ohlcv],
    btc_15m: &[Ohlcv],
    pair: &str,
) -> PairMetrics {
    let close_15m = candles_15m.last().map(|c| c.close).unwrap_or(0.0);
    let close_1h = candles_1h.last().map(|c| c.close).unwrap_or(0.0);
    let close_4h = candles_4h.last().map(|c| c.close).unwrap_or(0.0);
    // 1d close: use the return from 6 bars back on 4h candles (≈ 24h ago)
    let close_1d = if candles_4h.len() >= 7 {
        candles_4h[candles_4h.len() - 7].close
    } else {
        close_4h
    };

    let volume_15m = candles_15m.last().map(|c| c.volume).unwrap_or(0.0);
    let volume_1h = candles_1h.last().map(|c| c.volume).unwrap_or(0.0);
    let volume_4h = candles_4h.last().map(|c| c.volume).unwrap_or(0.0);
    // 1d volume: sum of the last 6 4h bars (~24h worth)
    let volume_1d: f64 = candles_4h.iter().rev().take(6).map(|c| c.volume).sum();

    let atr_14 = Indicators::atr(candles_15m, 14);
    let rsi_14 = Indicators::rsi(candles_15m, 14);
    let ema_20 = Indicators::ema20(candles_15m);
    let ema_50 = Indicators::ema50(candles_15m);
    let ema_200 = Indicators::ema200(candles_15m);
    let (macd_line, macd_signal, macd_histogram) = Indicators::macd(candles_15m);
    let vwap = Indicators::vwap(candles_15m);

    let coin_ret_15m = Indicators::return_since(candles_15m, 1);
    let coin_ret_1h = Indicators::return_since(candles_1h, 1);
    let coin_ret_4h = Indicators::return_since(candles_4h, 1);
    // 1d return: 6 4h-bars back = 24h
    let coin_ret_1d = Indicators::return_since(candles_4h, 6);

    // BTC returns from 15m candles (96 bars = 24h, 16 bars = 4h, 4 bars = 1h)
    let btc_ret_15m = Indicators::return_since(btc_15m, 1);
    let btc_ret_1h  = Indicators::return_since(btc_15m, 4);   // 4 × 15m = 1h
    let btc_ret_4h  = Indicators::return_since(btc_15m, 16);  // 16 × 15m = 4h
    let btc_ret_1d  = Indicators::return_since(btc_15m, 96);  // 96 × 15m = 24h

    let rs_15m = (coin_ret_15m - btc_ret_15m) * 100.0;
    let rs_1h  = (coin_ret_1h  - btc_ret_1h)  * 100.0;
    let rs_4h  = (coin_ret_4h  - btc_ret_4h)  * 100.0;
    let rs_1d  = (coin_ret_1d  - btc_ret_1d)  * 100.0;
    // Weights aligned with RSEngine: 1h=35%, 4h=30%, 1d=25%, 15m=10%
    let rs_score = rs_15m * 0.10 + rs_1h * 0.35 + rs_4h * 0.30 + rs_1d * 0.25;

    let volume_growth = Indicators::volume_growth(candles_15m, 20);
    let atr_expansion = if atr_14 > 0.0 {
        let prev_atr = Indicators::atr(&candles_15m[..candles_15m.len().saturating_sub(1)], 14);
        if prev_atr > 0.0 { (atr_14 - prev_atr) / prev_atr } else { 0.0 }
    } else { 0.0 };

    let ema_bullish_alignment = ema_20 > ema_50 && ema_50 > ema_200;
    let macd_bullish = macd_line > macd_signal && macd_histogram > 0.0;
    let volume_spike = volume_growth > 1.0;
    let volume_expansion = Indicators::is_volume_expansion(candles_15m, candles_1h, candles_4h);

    // Low-liquidity flag: BTC-quote pairs have tiny quote volumes (fractions of BTC).
    // Use count of zero-volume candles in last 10 instead of absolute threshold.
    let zero_vol_count = candles_15m.iter().rev().take(10).filter(|c| c.volume == 0.0).count();
    let low_liquidity = zero_vol_count >= 3;

    let wide_spread = false; // computed from orderbook in exchange layer, not here

    PairMetrics {
        pair: pair.to_string(),
        close_15m, close_1h, close_4h, close_1d,
        volume_15m, volume_1h, volume_4h, volume_1d,
        atr_14, atr_atr: atr_14,
        rsi_14, ema_20, ema_50, ema_200,
        macd_line, macd_signal, macd_histogram,
        vwap,
        bid_depth: 0.0, ask_depth: 0.0, spread_pct: 0.0,
        btc_return_15m: btc_ret_15m,
        btc_return_1h:  btc_ret_1h,
        btc_return_4h:  btc_ret_4h,
        btc_return_1d:  btc_ret_1d,
        rs_15m, rs_1h, rs_4h, rs_1d, rs_score,
        volume_growth, atr_expansion,
        ema_bullish_alignment, macd_bullish,
        volume_spike, volume_expansion,
        liquidity_growth: volume_growth > 0.0,
        wash_trade_detected: false,
        low_liquidity, wide_spread,
    }
}
