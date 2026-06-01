use crate::sanitize;

pub struct AppConfig {
    pub backend_port: u16,
    pub llm_url: String,
    pub llm_api_key: String,
    pub llm_model: String,
    pub data_dir: String,
    pub telegram_bot_token: String,
    pub telegram_whitelist: Vec<i64>,
    pub telegram_report_chat_ids: Vec<i64>,
    pub exchange_name: String,
    pub exchange_api_key: String,
    pub exchange_api_secret: String,
    pub exchange_base_url: String,
    pub wallet_password: String,
    pub scanner_interval_secs: u64,
    pub report_interval_mins: u64,
    pub scanner_pairs: Vec<String>,
}

impl AppConfig {
    pub fn load() -> Self {
        let _ = dotenvy::dotenv();

        if !std::path::Path::new("../.env").exists() {
            let _ = dotenvy::from_filename("../../../.env").ok();
            let _ = dotenvy::dotenv().ok();
        }

        let cwd = std::env::current_dir().unwrap_or_default();

        // Accept DATA_BTC_DIR (preferred, BTC-specific) or fall back to DATA_DIR
        // (shared with other services) so docker-compose and other tooling can use
        // a single env var. Without this fallback the docker compose DATA_DIR mount
        // is silently ignored, and all state is lost on container restart.
        let data_dir = {
            let raw = std::env::var("DATA_BTC_DIR")
                .ok()
                .filter(|v| !v.is_empty())
                .or_else(|| {
                    std::env::var("DATA_DIR")
                        .ok()
                        .filter(|v| !v.is_empty())
                });
            match raw {
                Some(p) => match sanitize::sanitize_path(&p, &cwd) {
                    Ok(sanitized) => sanitized.to_string_lossy().to_string(),
                    Err(e) => {
                        eprintln!("WARNING: {} — using default data directory", e);
                        "../data/btc-treasury".to_string()
                    }
                },
                None => "../data/btc-treasury".to_string(),
            }
        };

        // Scanner pairs: prefer BTC_SCANNER_PAIRS. Default to BTC-quote pairs
        // (ETHBTC, SOLBTC) — these are the pairs the bot actually trades for
        // BTC accumulation. BTCUSDT is the price reference, not a trade pair.
        let scanner_pairs = std::env::var("BTC_SCANNER_PAIRS")
            .ok()
            .filter(|s| !s.is_empty())
            .map(|s| {
                s.split(',')
                    .map(|p| p.trim().to_uppercase())
                    .filter(|p| !p.is_empty())
                    .collect::<Vec<_>>()
            })
            .unwrap_or_else(|| {
                vec!["ETHBTC".to_string(), "SOLBTC".to_string()]
            });

        Self {
            backend_port: env_u16("BTC_TREASURY_PORT", 8090),
            llm_url: env_str("LLM_URL", "https://api.openai.com/v1"),
            llm_api_key: std::env::var("LLM_API_KEY").unwrap_or_default(),
            llm_model: env_str("LLM_MODEL", "gpt-4o-mini"),
            data_dir,
            telegram_bot_token: env_str("TELEGRAM_BOT_BTC_TOKEN", ""),
            telegram_whitelist: env_whitelist("TELEGRAM_WHITELIST_USER_BTC_IDS"),
            telegram_report_chat_ids: env_whitelist("TELEGRAM_REPORT_CHAT_IDS"),
            exchange_name: env_str("EXCHANGE_NAME", "binance"),
            exchange_api_key: std::env::var("BINANCE_API_KEY")
                .or_else(|_| std::env::var("EXCHANGE_API_KEY"))
                .unwrap_or_default(),
            exchange_api_secret: std::env::var("BINANCE_API_SECRET")
                .or_else(|_| std::env::var("EXCHANGE_API_SECRET"))
                .unwrap_or_default(),
            exchange_base_url: env_str("EXCHANGE_BASE_URL", "https://api.binance.com"),
            wallet_password: std::env::var("WALLET_PASSWORD").unwrap_or_default(),
            scanner_interval_secs: env_u64("BTC_SCANNER_INTERVAL_SECS", 900),
            report_interval_mins: env_u64("BTC_REPORT_INTERVAL_MINS", 5),
            scanner_pairs,
        }
    }
}

fn env_str(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_string())
}

fn env_u16(key: &str, fallback: u16) -> u16 {
    std::env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(fallback)
}

fn env_u64(key: &str, fallback: u64) -> u64 {
    std::env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(fallback)
}

fn env_whitelist(key: &str) -> Vec<i64> {
    std::env::var(key)
        .ok()
        .map(|s| {
            s.split(',')
                .filter_map(|id| id.trim().parse().ok())
                .collect()
        })
        .unwrap_or_default()
}
