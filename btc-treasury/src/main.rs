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

    // Shared state
    let shared = server::init(&cfg).await?;

    // ── Fase 0: build the multi-exchange dispatcher from legacy env.
    // Only one `default` Binance account is supported today. The dispatcher
    // exposes the same `Arc<dyn ExchangeClient>` via `.default()` so the
    // existing scanner/monitor/bot call sites do not change.
    let account_specs = account_spec::load_account_specs(
        &cfg.exchange_name,
        cfg.scanner_pairs.clone(),
    );
    if let Err(e) = account_spec::validate(&account_specs) {
        tracing::error!("Invalid account spec: {}", e);
    }
    let dispatcher = MultiExchangeClient::from_specs(&account_specs);

    // Existing single-exchange call sites still hold `Option<Arc<dyn ExchangeClient>>`.
    // `.default()` is `None` when credentials are absent (advisory-only mode),
    // matching pre-Fase-0 behavior exactly.
    let exchange_client: Option<Arc<dyn ExchangeClient>> = dispatcher.default();

    // Sync treasury state with live Binance balances on startup. Without
    // this the local ledger stays at 0 and /btc_status shows zero BTC
    // holdings even when the account actually holds BTC. Also breaks
    // RiskManager::assess which reads `usdt_balance` to size positions.
    // Run before scanner/position-monitor spawn so the first risk calc
    // sees the real value.
    if let Some(ref exchange) = exchange_client {
        match exchange.get_balances().await {
            Ok(balances) => {
                let live_btc: f64 = balances.iter()
                    .find(|b| b.asset == "BTC")
                    .map(|b| b.free + b.locked)
                    .unwrap_or(0.0);
                let live_usdt: f64 = balances.iter()
                    .find(|b| b.asset == "USDT" || b.asset == "USDC")
                    .map(|b| b.free + b.locked)
                    .unwrap_or(0.0);
                shared.mem.sync_initial_balances(live_btc, live_usdt);
                // Refresh growth ratios so the freshly-synced previous_btc
                // becomes the anchor and risk calcs see a real btc_growth_7d.
                shared.mem.update_growth_ratios();
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

    // Scanner state
    let scanner_state = if exchange_client.is_some() {
        let state = Arc::new(ScannerState::new());

        // Initialize pairs from config (env var + saved JSON merged)
        let mem = shared.mem.clone();
        let mut pairs = mem.get_config().scanner_pairs.clone();
        for p in &cfg.scanner_pairs {
            if !pairs.contains(p) {
                pairs.push(p.clone());
            }
        }
        state.initialize_pairs(&pairs).await;
        {
            let mut saved_cfg = mem.get_config();
            saved_cfg.scanner_pairs = pairs;
            mem.save_config(&saved_cfg);
        }

        let exchange = Arc::clone(exchange_client.as_ref().unwrap());
        let engine = Arc::clone(&shared.engine);
        let mem = Arc::clone(&mem);
        let interval = cfg.scanner_interval_secs;
        let scanner = Arc::clone(&state);

        let executor = Arc::new(ExecutionEngine::new(
            Some(Arc::clone(exchange_client.as_ref().unwrap())),
            mem.clone(),
        ));

        tokio::spawn(async move {
            scanner::run(scanner, exchange, engine, executor, mem, interval).await;
        });

        Some(state)
    } else {
        tracing::warn!("Scanner disabled — exchange API key not configured");
        None
    };

    // Position Monitor (TP/SL checker)
    if exchange_client.is_some() {
        let mem = Arc::clone(&shared.mem);
        let exchange = Arc::clone(exchange_client.as_ref().unwrap());
        let engine = Arc::clone(&shared.engine);
        let monitor = Arc::new(PositionMonitor::new(mem, Some(exchange), engine));
        tokio::spawn(async move {
            monitor.start().await;
        });
        tracing::info!("BTC Position Monitor started");
    }

    // Reporter
    if !cfg.telegram_report_chat_ids.is_empty() {
        let state = scanner_state.clone().unwrap_or_else(|| Arc::new(ScannerState::new()));
        let mem = Arc::clone(&shared.mem);
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
        let bot = Arc::new(telegram_bot::BtcBot::new(
            cfg.telegram_bot_token.clone(),
            cfg.telegram_whitelist.clone(),
            shared.engine.clone(),
            shared.mem.clone(),
            exchange_client.clone(),
            scanner_state.clone(),
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
