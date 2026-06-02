//! Per-account runtime (Fase 1).
//!
//! One `AccountRuntime` is the owned bundle of `MemoryStore + scanner +
//! position-monitor + ExecutionEngine + reporter` for a single (exchange,
//! account_id). `main.rs` builds one runtime per `AccountSpec` and `spawn`s
//! the scanner, monitor, and per-account reporter onto the tokio runtime.
//! `MultiExchangeClient` continues to provide the cross-account client
//! lookup (`for_account(key)`) so the Telegram bot can route commands to
//! the active account per chat.

use std::sync::Arc;

use crate::account_spec::AccountSpec;
use crate::exchange::ExchangeClient;
use crate::execution_engine::ExecutionEngine;
use crate::memory::MemoryStore;
use crate::position_monitor::PositionMonitor;
use crate::scanner::ScannerState;

/// Per-account runtime. Owns the per-account `MemoryStore`, scanner state,
/// and live task handles. Built once in `main.rs` per `AccountSpec`.
pub struct AccountRuntime {
    pub account_id: String,
    pub exchange: Arc<dyn ExchangeClient>,
    pub mem: Arc<MemoryStore>,
    pub scanner_state: Arc<ScannerState>,
    pub executor: Arc<ExecutionEngine>,
}

impl AccountRuntime {
    /// Build a runtime for a single account. Pairs must already be initialized
    /// by the caller via `initialize_pairs` (which is async). The
    /// `non_async_init` helper below provides a sync version that uses
    /// `tokio::runtime::Handle::current().block_on` — call it from a tokio
    /// context (which `main.rs` always is).
    pub fn build(
        spec: &AccountSpec,
        exchange: Arc<dyn ExchangeClient>,
        data_dir: &str,
    ) -> Self {
        let mem = Arc::new(MemoryStore::with_account(data_dir, Some(&spec.id)));
        let scanner_state = Arc::new(ScannerState::new());
        let executor = Arc::new(ExecutionEngine::new(Some(Arc::clone(&exchange)), mem.clone()));

        // Initialize scanner pairs from the account spec. The spec's
        // `scanner_pairs` is the authoritative list for this account
        // (overrides the global env-provided default). This is async because
        // ScannerState uses `tokio::sync::RwLock`; we block on it from a
        // tokio context.
        let pairs = if spec.scanner_pairs.is_empty() {
            mem.get_config().scanner_pairs
        } else {
            spec.scanner_pairs.clone()
        };
        let handle = tokio::runtime::Handle::current();
        handle.block_on(scanner_state.initialize_pairs(&pairs));
        {
            let mut saved_cfg = mem.get_config();
            saved_cfg.scanner_pairs = pairs;
            mem.save_config(&saved_cfg);
        }

        Self {
            account_id: spec.id.clone(),
            exchange,
            mem,
            scanner_state,
            executor,
        }
    }

    /// Build a position monitor for this account. Caller spawns it.
    pub fn build_monitor(&self, engine: Arc<crate::engine::AdvisoryEngine>) -> Arc<PositionMonitor> {
        Arc::new(PositionMonitor::new(
            self.mem.clone(),
            Some(Arc::clone(&self.exchange)),
            engine,
        ))
    }
}
