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

        let data_dir = match std::env::var("DATA_BTC_DIR") {
            Ok(raw) if !raw.is_empty() => match sanitize::sanitize_path(&raw, &cwd) {
                Ok(p) => p.to_string_lossy().to_string(),
                Err(e) => {
                    eprintln!("WARNING: {} — using default data directory", e);
                    "../data/btc-treasury".to_string()
                }
            },
            _ => "../data/btc-treasury".to_string(),
        };

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
            exchange_api_key: std::env::var("EXCHANGE_API_KEY").unwrap_or_default(),
            exchange_api_secret: std::env::var("EXCHANGE_API_SECRET").unwrap_or_default(),
            exchange_base_url: env_str("EXCHANGE_BASE_URL", "https://api.binance.com"),
            scanner_interval_secs: env_u64("BTC_SCANNER_INTERVAL_SECS", 30),
            report_interval_mins: env_u64("BTC_REPORT_INTERVAL_MINS", 5),
            scanner_pairs: env_pairs("BTC_SCANNER_PAIRS", "BTCUSDT"),
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

fn env_pairs(key: &str, fallback: &str) -> Vec<String> {
    std::env::var(key)
        .ok()
        .filter(|s| !s.is_empty())
        .map(|s| {
            s.split(',')
                .map(|p| p.trim().to_uppercase())
                .filter(|p| !p.is_empty())
                .collect()
        })
        .unwrap_or_else(|| {
            fallback
                .split(',')
                .map(|p| p.trim().to_uppercase())
                .filter(|p| !p.is_empty())
                .collect()
        })
}
