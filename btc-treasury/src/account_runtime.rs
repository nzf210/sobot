//! Per-account runtime (Fase 1, expanded in Fase 3, hardened in Fase 4).
//!
//! One `AccountRuntime` is the owned bundle of `MemoryStore + scanner +
//! position-monitor + ExecutionEngine + reporter` for a single
//! `(exchange, account_id)` tuple. `main.rs` builds one runtime per
//! `AccountSpec` and `spawn`s the scanner, monitor, and per-account reporter
//! onto the tokio runtime. `MultiExchangeClient` continues to provide the
//! cross-account client lookup (`for_account(key)`) so the Telegram bot can
//! route commands to the active account per chat.
//!
//! Fase 3 adds `pub key: AccountKey` and `pub spec: AccountSpec` so callers
//! (bot, reporter, main) can identify which `(id, exchange)` a runtime
//! represents without re-parsing the spec list.
//!
//! Fase 4 adds `pub status: Arc<AccountStatus>` — a lightweight heartbeat +
//! restart counter shared between the supervisor loop in `main.rs` and the
//! `GET /btc/accounts` HTTP endpoint in `server.rs`.

use std::sync::Arc;
use std::sync::atomic::{AtomicI64, AtomicU32, AtomicBool, Ordering};

use crate::account_spec::AccountSpec;
use crate::exchange::ExchangeClient;
use crate::execution_engine::ExecutionEngine;
use crate::memory::MemoryStore;
use crate::multi_exchange::AccountKey;
use crate::position_monitor::PositionMonitor;
use crate::scanner::ScannerState;

/// Runtime health for one `(exchange, account_id)` binding.
///
/// Written by the supervisor loop in `main.rs`, read by
/// `GET /btc/accounts` in `server.rs` and `/btc_status` in Telegram.
///
/// `last_heartbeat_unix` is the Unix timestamp (seconds) of the last
/// successful scanner tick. `restart_count` increments each time the
/// supervisor restarts the inner task after a panic. Both are atomics so
/// they can be updated from spawned tasks without locking.
pub struct AccountStatus {
    /// Unix timestamp (seconds) of the last scanner heartbeat. 0 = never.
    pub last_heartbeat_unix: AtomicI64,
    /// Number of times the supervisor has restarted this account's tasks.
    pub restart_count: AtomicU32,
    /// Whether the scanner and position monitor are active.
    pub enabled: AtomicBool,
}

impl AccountStatus {
    pub fn new(enabled: bool) -> Self {
        Self {
            last_heartbeat_unix: AtomicI64::new(0),
            restart_count: AtomicU32::new(0),
            enabled: AtomicBool::new(enabled),
        }
    }

    pub fn is_enabled(&self) -> bool {
        self.enabled.load(Ordering::Relaxed)
    }

    pub fn set_enabled(&self, enabled: bool) {
        self.enabled.store(enabled, Ordering::Relaxed);
    }

    /// Record a heartbeat — call from the scanner loop each tick.
    pub fn touch(&self) {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0);
        self.last_heartbeat_unix.store(now, Ordering::Relaxed);
    }

    pub fn heartbeat_unix(&self) -> i64 {
        self.last_heartbeat_unix.load(Ordering::Relaxed)
    }

    pub fn restarts(&self) -> u32 {
        self.restart_count.load(Ordering::Relaxed)
    }

    pub fn increment_restart(&self) {
        self.restart_count.fetch_add(1, Ordering::Relaxed);
    }
}

/// Per-account runtime. Owns the per-account `MemoryStore`, scanner state,
/// and live task handles. Built once in `main.rs` per `AccountSpec`.
pub struct AccountRuntime {
    pub key: AccountKey,
    pub spec: AccountSpec,
    pub account_id: String,
    pub exchange: Arc<dyn ExchangeClient>,
    pub mem: Arc<MemoryStore>,
    pub scanner_state: Arc<ScannerState>,
    pub executor: Arc<ExecutionEngine>,
    pub engine: Arc<crate::engine::AdvisoryEngine>,
    /// Live health counters — updated by the supervisor, read by HTTP/Telegram.
    pub status: Arc<AccountStatus>,
}

impl AccountRuntime {
    /// Build a runtime for a single account. The returned runtime is
    /// fully constructed but its scanner pairs are NOT yet loaded — call
    /// `initialize_pairs_async` afterwards from an async context.
    ///
    /// Why split: `ScannerState::initialize_pairs` takes `&self` and
    /// `&[String]` and uses `tokio::sync::RwLock`, so it MUST be called
    /// from an async context. The previous version called
    /// `Handle::current().block_on(...)` from inside this `fn build`, but
    /// `main.rs` is `#[tokio::main]` — blocking the current thread from
    /// within a tokio runtime panics with "Cannot start a runtime from
    /// within a runtime". The fix is to keep `build` synchronous and
    /// expose an async init step the caller awaits.
    pub fn build(
        spec: &AccountSpec,
        exchange: Arc<dyn ExchangeClient>,
        data_dir: &str,
        llm_url: &str,
        llm_model: &str,
        llm_api_key: &str,
    ) -> Self {
        // Pass `Some(spec.exchange)` so MemoryStore knows which (id, exchange)
        // this runtime belongs to. The default-account flat-layout special
        // case in MemoryStore handles the "1 account, 2 exchanges" backward
        // compat path: with id=default, the layered subdir is skipped.
        let mem = Arc::new(MemoryStore::with_account(
            data_dir,
            Some(&spec.id),
            Some(spec.exchange),
        ));
        let scanner_state = Arc::new(ScannerState::new());
        let executor = Arc::new(ExecutionEngine::new(Some(Arc::clone(&exchange)), mem.clone()));
        let engine = Arc::new(crate::engine::AdvisoryEngine::new(
            llm_url.to_string(),
            llm_model.to_string(),
            llm_api_key.to_string(),
            mem.clone(),
        ));

        let key = AccountKey::from_spec(spec);
        let status = Arc::new(AccountStatus::new(spec.enabled));
        Self {
            key,
            spec: spec.clone(),
            account_id: spec.id.clone(),
            exchange,
            mem,
            scanner_state,
            executor,
            engine,
            status,
        }
    }

    /// Async pair-initialization step. Must be awaited from an async
    /// context (i.e. from `main.rs`'s `#[tokio::main]` body, NOT from
    /// inside `build`). Persists the resolved pair list to disk so the
    /// next startup restores it without re-running the resolution logic.
    pub async fn initialize_pairs_async(&self) {
        // Resolve effective pairs: spec override > persisted config > empty.
        let pairs = if self.spec.scanner_pairs.is_empty() {
            self.mem.get_config().scanner_pairs
        } else {
            self.spec.scanner_pairs.clone()
        };
        self.scanner_state.initialize_pairs(&pairs).await;
        {
            let mut saved_cfg = self.mem.get_config();
            saved_cfg.scanner_pairs = pairs;
            self.mem.save_config(&saved_cfg);
        }
    }

    /// Build a position monitor for this account. Caller spawns it.
    pub fn build_monitor(&self) -> Arc<PositionMonitor> {
        let label = format!("{}/{}", self.spec.exchange.as_str(), self.account_id);
        Arc::new(PositionMonitor::new(
            self.mem.clone(),
            Some(Arc::clone(&self.exchange)),
            Some(Arc::clone(&self.status)),
        ).with_label(label))
    }
}
