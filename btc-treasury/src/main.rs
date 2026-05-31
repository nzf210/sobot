mod binance;
mod config;
mod crypto;
mod engines;
mod engine;
mod exchange;
mod execution_engine;
mod format;
mod hyperliquid;
mod indicators;
mod llm;
mod memory;
mod models;
mod position_monitor;
mod reporter;
mod sanitize;
mod scanner;
mod server;
mod telegram_bot;

use std::sync::Arc;

use tracing_subscriber::EnvFilter;

use crate::binance::BinanceClient;
use crate::exchange::ExchangeClient;
use crate::hyperliquid::HyperliquidClient;
use crate::position_monitor::PositionMonitor;
use crate::scanner::ScannerState;

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env())
        .init();

    let cfg = config::AppConfig::load();

    // Shared state
    let shared = server::run(&cfg).await?;

    // Exchange client (Binance or Hyperliquid based on config)
    let exchange_client: Option<Arc<dyn ExchangeClient>> = if cfg.exchange_api_key.is_empty() || cfg.exchange_api_secret.is_empty() {
        tracing::warn!("Exchange API key/secret not configured — running advisory-only");
        None
    } else {
        match cfg.exchange_name.as_str() {
            "hyperliquid" => {
                // Try loading from encrypted file first, then fall back to env vars
                let enc_path = std::path::Path::new(&cfg.hyperliquid_key_path);
                let base_url = if cfg.exchange_base_url.is_empty() {
                    None
                } else {
                    Some(cfg.exchange_base_url.clone())
                };

                let client = if enc_path.exists() && !cfg.wallet_password.is_empty() {
                    match HyperliquidClient::load_from_encrypted_file(enc_path, &cfg.wallet_password, base_url.clone()) {
                        Ok((_key, client)) => {
                            tracing::info!("Hyperliquid: loaded from encrypted file (address: {})", client.api_key_display());
                            client
                        }
                        Err(e) => {
                            tracing::error!("Failed to load hyperliquid.enc: {} — falling back to env vars", e);
                            HyperliquidClient::new(cfg.exchange_api_key.clone(), cfg.exchange_api_secret.clone(), base_url)
                        }
                    }
                } else {
                    HyperliquidClient::new(cfg.exchange_api_key.clone(), cfg.exchange_api_secret.clone(), base_url)
                };

                tracing::info!("Hyperliquid client initialized (address: {})", client.api_key_display());
                Some(Arc::new(client) as Arc<dyn ExchangeClient>)
            }
            _ => {
                let base_url = if cfg.exchange_base_url.is_empty() {
                    None
                } else {
                    Some(cfg.exchange_base_url.clone())
                };
                let client = BinanceClient::new(
                    cfg.exchange_api_key.clone(),
                    cfg.exchange_api_secret.clone(),
                    base_url,
                );
                tracing::info!("Binance client initialized (API key: {})", client.api_key_display());
                Some(Arc::new(client) as Arc<dyn ExchangeClient>)
            }
        }
    };

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

        tokio::spawn(async move {
            scanner::run(scanner, exchange, engine, mem, interval).await;
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

    // Keep process alive (server runs in background)
    loop {
        tokio::time::sleep(std::time::Duration::from_secs(3600)).await;
    }
}
