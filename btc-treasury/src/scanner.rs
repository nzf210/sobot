use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use tokio::sync::RwLock;
use tokio::time::{interval, Duration};

use crate::engine::AdvisoryEngine;
use crate::exchange::{ExchangeClient, ExchangeOrderResult};
use crate::memory::MemoryStore;
use crate::models::*;

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
    mem: Arc<MemoryStore>,
    interval_secs: u64,
) {
    let mut tick = interval(Duration::from_secs(interval_secs));
    tracing::info!("Multi-pair scanner started (every {}s) on {}", interval_secs, exchange.exchange_name());

    loop {
        tick.tick().await;
        let pairs = state.get_pairs().await;
        if pairs.is_empty() {
            tracing::warn!("Scanner: no pairs configured");
            continue;
        }

        for pair in &pairs {
            if let Some(ps) = state.get_pair_state(pair).await {
                scan_pair(&state, pair, &ps, &*exchange, &engine, &mem).await;
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

    let stored_positions = mem.get_positions();
    let loss_streak = {
        let mut streak = 0;
        for pos in stored_positions.iter().rev() {
            if pos.pnl_btc < 0.0 {
                streak += 1;
            } else {
                break;
            }
        }
        streak
    };

    let input = BtcAdvisoryInput {
        market_data: market_data.clone(),
        treasury: treasury.clone(),
        open_positions: open_orders,
        loss_streak,
    };

    let advisory = engine.analyze(&input).await;

    *ps.last_regime.write().await = advisory.market_regime.clone();
    *ps.last_recommendation.write().await = advisory.recommendation.clone();
    *ps.last_reason.write().await = advisory.reason.clone();
    *ps.last_confidence.write().await = advisory.confidence;
    *ps.last_risk_level.write().await = advisory.risk_level.clone();

    match advisory.recommendation.as_str() {
        "APPROVE" => {
            ps.stats.advisory_approve.fetch_add(1, Ordering::Relaxed);
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
