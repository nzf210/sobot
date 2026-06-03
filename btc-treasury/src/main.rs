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
mod okx;
mod position_monitor;
mod reporter;
mod sanitize;
mod scanner;
mod server;
mod telegram_bot;
mod util;

use std::sync::Arc;

use tracing_subscriber::EnvFilter;

use crate::account_runtime::AccountRuntime;
use crate::multi_exchange::MultiExchangeClient;
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

    // ── Fase 3: load all account specs. Three sources, in priority order:
    //   1. BTC_ACCOUNTS_JSON env var (raw JSON string)
    //   2. data_dir/btc-accounts.json or data_dir/accounts/{id}/accounts.json
    //   3. Legacy env-var fan-out via EXCHANGE_NAME=both / binance,okx / binance
    // Two specs sharing the same `id` (one Binance + one OKX) is the
    // "1 account, 2 exchanges" model.
    let account_specs = account_spec::load_account_specs(
        &cfg.exchange_name,
        cfg.scanner_pairs.clone(),
    );
    if let Err(e) = account_spec::validate(&account_specs) {
        tracing::error!("Invalid account spec: {}", e);
    }
    if account_specs.len() > 1
        && account_specs.iter().any(|s| s.id == "default")
    {
        tracing::warn!(
            "Multiple account specs share id='default' — they will share the legacy \
             flat layout at data_dir/. Create a named id (e.g. 'main') in accounts.json \
             to isolate per-exchange state."
        );
    }

    // Build the dispatcher and the per-account runtimes. The dispatcher is
    // the lookup table for the Telegram bot / HTTP server.
    let dispatcher = MultiExchangeClient::from_specs(&account_specs);

    // Build one runtime per spec. Each gets its own scanner + monitor + mem.
    // With N specs, the loop spawns N scanner tasks and N monitor tasks.
    let mut runtimes: Vec<Arc<AccountRuntime>> = Vec::new();
    for spec in &account_specs {
        let key = crate::multi_exchange::AccountKey::from_spec(spec);
        let Some(exchange) = dispatcher.for_account(&key) else {
            tracing::warn!(
                "Skipping spec {}/{} — dispatcher could not build a client (credentials unresolved?)",
                spec.id, spec.exchange.as_str()
            );
            continue;
        };
        let rt = Arc::new(AccountRuntime::build(spec, exchange, &cfg.data_dir));

        // Sync initial balances for THIS runtime. With multiple exchanges
        // under one id, each runtime syncs against its own exchange so the
        // local ledger matches the live balance per exchange.
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
                    "Failed to fetch live balances for treasury sync ({}/{}): {} — \
                     btc-treasury.json will keep its existing (likely 0.0) values until next close",
                    spec.id, spec.exchange.as_str(), e
                );
            }
        }

        // Spawn scanner + monitor for this runtime, wrapped in a supervisor
        // loop that restarts on panic with exponential backoff (cap 5 min).
        // After MAX_RESTARTS_BEFORE_ALERT consecutive restarts, a warning is
        // logged; caller can wire a Telegram alert here in the future.
        const MAX_RESTARTS_BEFORE_ALERT: u32 = 3;

        {
            let exchange = Arc::clone(&rt.exchange);
            let engine_c = Arc::clone(&shared.engine);
            let mem_c = rt.mem.clone();
            let interval_c = cfg.scanner_interval_secs;
            let scanner_c = Arc::clone(&rt.scanner_state);
            let executor_c = Arc::clone(&rt.executor);
            let status_c = Arc::clone(&rt.status);
            let account_id_c = spec.id.clone();
            let exchange_name_c = spec.exchange.as_str().to_string();
            tokio::spawn(async move {
                let mut backoff_secs: u64 = 5;
                loop {
                    let ex2 = Arc::clone(&exchange);
                    let eng2 = Arc::clone(&engine_c);
                    let mem2 = mem_c.clone();
                    let sc2 = Arc::clone(&scanner_c);
                    let ex2c = Arc::clone(&executor_c);
                    let st2 = Arc::clone(&status_c);
                    let handle = tokio::spawn(async move {
                        scanner::run(sc2, ex2, eng2, ex2c, mem2, interval_c, st2).await;
                    });
                    match handle.await {
                        Ok(_) => break, // clean exit
                        Err(e) if e.is_panic() => {
                            let restarts = status_c.restarts();
                            status_c.increment_restart();
                            if restarts >= MAX_RESTARTS_BEFORE_ALERT {
                                tracing::error!(
                                    account_id = %account_id_c,
                                    exchange = %exchange_name_c,
                                    restarts = restarts + 1,
                                    "Scanner panicked {} times — restarting in {}s",
                                    restarts + 1, backoff_secs
                                );
                            } else {
                                tracing::warn!(
                                    account_id = %account_id_c,
                                    exchange = %exchange_name_c,
                                    "Scanner panicked — restarting in {}s", backoff_secs
                                );
                            }
                            tokio::time::sleep(tokio::time::Duration::from_secs(backoff_secs)).await;
                            backoff_secs = (backoff_secs * 2).min(300); // cap 5 min
                        }
                        Err(e) => {
                            tracing::error!(
                                account_id = %account_id_c,
                                exchange = %exchange_name_c,
                                "Scanner task join error: {} — not restarting", e
                            );
                            break;
                        }
                    }
                }
            });
        }
        {
            let engine_m = Arc::clone(&shared.engine);
            let status_m = Arc::clone(&rt.status);
            let account_id_m = spec.id.clone();
            let exchange_name_m = spec.exchange.as_str().to_string();
            let monitor = rt.build_monitor(engine_m);
            tokio::spawn(async move {
                let mut backoff_secs: u64 = 5;
                loop {
                    let mon2 = Arc::clone(&monitor);
                    let handle = tokio::spawn(async move {
                        mon2.start().await;
                    });
                    match handle.await {
                        Ok(_) => break,
                        Err(e) if e.is_panic() => {
                            status_m.increment_restart();
                            tracing::warn!(
                                account_id = %account_id_m,
                                exchange = %exchange_name_m,
                                "Monitor panicked — restarting in {}s", backoff_secs
                            );
                            tokio::time::sleep(tokio::time::Duration::from_secs(backoff_secs)).await;
                            backoff_secs = (backoff_secs * 2).min(300);
                        }
                        Err(e) => {
                            tracing::error!(
                                account_id = %account_id_m,
                                exchange = %exchange_name_m,
                                "Monitor task join error: {} — not restarting", e
                            );
                            break;
                        }
                    }
                }
            });
        }
        tracing::info!(
            "BTC scanner + monitor started for {}/{}",
            spec.id, spec.exchange.as_str()
        );
        runtimes.push(rt);
    }

    if runtimes.is_empty() {
        tracing::warn!("Scanner disabled — no exchange API key configured");
    }

    // Per-account runtime map keyed by `(exchange, account_id)`. The bot's
    // `chat_id → AccountKey` lookup resolves commands to a single runtime.
    // Aggregate commands iterate this map. With one `default` account the
    // map has one entry, so single-account users see byte-identical behavior.
    let per_account: std::collections::HashMap<crate::multi_exchange::AccountKey, Arc<AccountRuntime>> =
        runtimes.iter().map(|r| (r.key.clone(), Arc::clone(r))).collect();

    // Pick one scanner state for the bot's "single scanner stats" view
    // (used by the legacy `/btc_scan` path that doesn't take an exchange
    // arg). First runtime wins; with one account this is the only runtime.
    let scanner_state_for_bot: Option<Arc<ScannerState>> = runtimes.first()
        .map(|r| Arc::clone(&r.scanner_state));

    // Reporter — per-(id, exchange) loop. Each runtime emits its own report
    // prefixed with `[id/exchange]` (or just `[id]` when only one exchange
    // exists for that id). The aggregate footer sums across all runtimes
    // and is sent to the first runtime's chat list to avoid spamming every
    // chat with the same digest.
    if !cfg.telegram_report_chat_ids.is_empty() {
        let mut reports = Vec::new();
        for (rt, spec) in runtimes.iter().zip(account_specs.iter()) {
            let mut chats = spec.telegram_chat_ids.clone();
            if chats.is_empty() {
                chats = cfg.telegram_report_chat_ids.clone();
            }
            reports.push(reporter::PerAccountReport {
                account_id: spec.id.clone(),
                exchange: spec.exchange,
                state: rt.scanner_state.clone(),
                mem: rt.mem.clone(),
                chat_ids: chats,
            });
        }
        if !reports.is_empty() {
            let token = cfg.telegram_bot_token.clone();
            let fallback = cfg.telegram_report_chat_ids.clone();
            let interval = cfg.report_interval_mins;
            tokio::spawn(async move {
                reporter::run(reports, token, fallback, interval).await;
            });
        } else {
            tracing::warn!("Reporter disabled — no per-account runtimes available");
        }
    } else {
        tracing::warn!("TELEGRAM_REPORT_CHAT_IDS not set — reporter disabled");
    }

    // Telegram Bot
    if !cfg.telegram_bot_token.is_empty() {
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
