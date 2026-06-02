mod account_runtime;
mod account_spec;
mod binance;
mod config;
mod engines;
mod engine;
mod exchange;
mod execution_engine;
mod format;
mod indicators;
mod llm;
mod memory;
mod models;
mod multi_exchange;
mod position_monitor;
mod reporter;
mod sanitize;
mod scanner;
mod server;
mod telegram_bot;

use std::sync::Arc;

use tracing_subscriber::EnvFilter;

use crate::account_runtime::AccountRuntime;
use crate::exchange::ExchangeClient;
use crate::execution_engine::ExecutionEngine;
use crate::multi_exchange::MultiExchangeClient;
use crate::position_monitor::PositionMonitor;
use crate::scanner::ScannerState;

#[tokio::main]
async fn main() -> std::io::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env())
        .init();

    let cfg = config::AppConfig::load();

    // Shared state — the legacy default account's memory + engine. We keep
    // these in `BotShared` so server.rs / telegram_bot.rs (which were written
    // pre-Fase-1) don't need refactoring. With multiple accounts, the bot
    // will additionally receive the per-account runtimes and route commands
    // to the chat's active account.
    let shared = server::init(&cfg).await?;

    // ── Fase 1: load all account specs (legacy default + any from env/JSON).
    // Legacy env (BINANCE_API_KEY/BINANCE_API_SECRET) yields exactly one
    // `default` spec, identical to pre-Fase-1 behavior. Multi-account config
    // (BTC_ACCOUNTS_JSON, Fase 1.5) can produce N specs; the loop below
    // spawns one scanner + monitor + reporter per spec.
    let account_specs = account_spec::load_account_specs(
        &cfg.exchange_name,
        cfg.scanner_pairs.clone(),
    );
    if let Err(e) = account_spec::validate(&account_specs) {
        tracing::error!("Invalid account spec: {}", e);
    }

    // Build the dispatcher and the per-account runtimes in parallel. The
    // dispatcher is the lookup table for the Telegram bot / HTTP server.
    let dispatcher = MultiExchangeClient::from_specs(&account_specs);

    // Legacy path: a single `default` account means the BotShared mem/engine
    // is the active account. We mirror that account's MemoryStore into the
    // BotShared struct so server.rs continues to work without a refactor.
    let default_runtime: Option<AccountRuntime> = if let Some(spec) = account_specs.first() {
        let key = crate::multi_exchange::AccountKey::from_spec(spec);
        dispatcher
            .for_account(&key)
            .map(|exchange| {
                AccountRuntime::build(spec, exchange, &cfg.data_dir)
            })
    } else {
        None
    };

    // If we have a default runtime, sync its treasury with the live balances
    // (same behavior as pre-Fase-1). This is also what `sync_initial_balances`
    // depends on so the first risk calc sees a real value, not zero.
    if let Some(rt) = default_runtime.as_ref() {
        match rt.exchange.get_balances().await {
            Ok(balances) => {
                let live_btc: f64 = balances.iter()
                    .find(|b| b.asset == "BTC")
                    .map(|b| b.free + b.locked)
                    .unwrap_or(0.0);
                let live_usdt: f64 = balances.iter()
                    .find(|b| b.asset == "USDT" || b.asset == "USDC")
                    .map(|b| b.free + b.locked)
                    .unwrap_or(0.0);
                rt.mem.sync_initial_balances(live_btc, live_usdt);
                rt.mem.update_growth_ratios();
            }
            Err(e) => {
                tracing::error!(
                    "Failed to fetch live balances for treasury sync: {} — \
                     btc-treasury.json will keep its existing (likely 0.0) values until next close",
                    e
                );
            }
        }
    }

    // Scanner + monitor spawn (one per account, with a default-account fast
    // path that preserves the exact pre-Fase-1 log messages and code path).
    let scanner_state_for_bot: Option<Arc<ScannerState>> = if let Some(rt) = default_runtime.as_ref() {
        let exchange = Arc::clone(&rt.exchange);
        let engine = Arc::clone(&shared.engine);
        let mem = rt.mem.clone();
        let interval = cfg.scanner_interval_secs;
        let scanner = Arc::clone(&rt.scanner_state);
        let executor = Arc::clone(&rt.executor);

        tokio::spawn(async move {
            scanner::run(scanner, exchange, engine, executor, mem, interval).await;
        });

        // Position monitor
        let monitor = rt.build_monitor(Arc::clone(&shared.engine));
        tokio::spawn(async move {
            monitor.start().await;
        });
        tracing::info!("BTC Position Monitor started");

        Some(rt.scanner_state.clone())
    } else {
        tracing::warn!("Scanner disabled — exchange API key not configured");
        None
    };

    // Reporter
    if !cfg.telegram_report_chat_ids.is_empty() {
        let state = scanner_state_for_bot.clone().unwrap_or_else(|| Arc::new(ScannerState::new()));
        let mem = if let Some(rt) = default_runtime.as_ref() {
            rt.mem.clone()
        } else {
            shared.mem.clone()
        };
        let token = cfg.telegram_bot_token.clone();
        let chat_ids = cfg.telegram_report_chat_ids.clone();
        let interval = cfg.report_interval_mins;

        tokio::spawn(async move {
            reporter::run(state, mem, token, chat_ids, interval).await;
        });
    } else {
        tracing::warn!("TELEGRAM_REPORT_CHAT_IDS not set — reporter disabled");
    }

    // Telegram Bot
    if !cfg.telegram_bot_token.is_empty() {
        // Build the per-account map for multi-account routing. With one
        // `default` account, the bot behaves identically to pre-Fase-1
        // because every chat's active account resolves to `default`.
        let per_account = build_per_account_map(&account_specs, &dispatcher, &cfg.data_dir, &shared.engine);
        let bot = Arc::new(telegram_bot::BtcBot::new(
            cfg.telegram_bot_token.clone(),
            cfg.telegram_whitelist.clone(),
            shared.engine.clone(),
            shared.mem.clone(),
            dispatcher.default(),
            scanner_state_for_bot.clone(),
            per_account,
        ));
        tracing::info!("BTC Telegram bot starting...");
        tokio::spawn(async move {
            bot.start().await;
        });
    } else {
        tracing::warn!("TELEGRAM_BOT_BTC_TOKEN not set — Telegram bot disabled");
    }

    // Graceful shutdown: catch SIGINT (Ctrl-C) and SIGTERM (docker stop, kubectl
    // rolling update). Previously the process held an infinite `sleep(3600)`,
    // so docker would force-kill after the stop timeout and any in-flight
    // scanner cycle or atomic-rename could be torn mid-flight.
    let shutdown = tokio::signal::ctrl_c();
    #[cfg(unix)]
    let shutdown = {
        let ctrl_c = shutdown;
        let mut sigterm = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("failed to install SIGTERM handler");
        async move {
            tokio::select! {
                _ = ctrl_c => "SIGINT",
                _ = sigterm.recv() => "SIGTERM",
            }
        }
    };
    let signal = shutdown.await;
    tracing::info!("Received {} — initiating graceful shutdown", signal);
    // Give in-flight async tasks a moment to settle, then exit. JSON writes
    // are atomic so any state already on disk is consistent.
    tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    tracing::info!("BTC Treasury shut down cleanly");
    Ok(())
}

/// Build a map of `account_id → AccountRuntime` for the Telegram bot's
/// per-account routing. With one `default` account the map has one entry;
/// with N accounts it has N. The bot stores `chat_id → active_account_id`
/// in its own state and resolves commands against this map.
fn build_per_account_map(
    specs: &[crate::account_spec::AccountSpec],
    dispatcher: &MultiExchangeClient,
    data_dir: &str,
    _engine: &Arc<crate::engine::AdvisoryEngine>,
) -> std::collections::HashMap<String, Arc<AccountRuntime>> {
    let mut map = std::collections::HashMap::new();
    for spec in specs {
        let key = crate::multi_exchange::AccountKey::from_spec(spec);
        if let Some(exchange) = dispatcher.for_account(&key) {
            let rt = AccountRuntime::build(spec, exchange, data_dir);
            map.insert(spec.id.clone(), Arc::new(rt));
        }
    }
    map
}
