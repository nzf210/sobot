//! Multi-account & multi-CEX account specifications (Fase 0).
//!
//! Fase 0 introduces the data model and loader only — no runtime behavior
//! change. Existing `BINANCE_API_KEY`/`BINANCE_API_SECRET` env vars continue
//! to drive a single `default` Binance account, identical to today.

use std::collections::HashSet;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ExchangeKind {
    Binance,
    Okx,
}

impl ExchangeKind {
    pub fn from_str(s: &str) -> Option<Self> {
        match s.trim().to_lowercase().as_str() {
            "binance" => Some(ExchangeKind::Binance),
            "okx" => Some(ExchangeKind::Okx),
            _ => None,
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            ExchangeKind::Binance => "binance",
            ExchangeKind::Okx => "okx",
        }
    }
}

/// Credentials source. Fase 0 only uses `EnvKeySecret`; `Inline` exists for tests.
#[derive(Debug, Clone)]
pub enum Credentials {
    EnvKeySecret {
        key_env: String,
        secret_env: String,
        passphrase_env: Option<String>,
    },
    Inline {
        api_key: String,
        api_secret: String,
        passphrase: Option<String>,
    },
}

impl Credentials {
    /// Resolve to (api_key, api_secret, passphrase) at startup. `None` passphrase is OK.
    pub fn resolve(&self) -> Result<(String, String, Option<String>), String> {
        match self {
            Credentials::EnvKeySecret { key_env, secret_env, passphrase_env } => {
                let key = std::env::var(key_env)
                    .map_err(|_| format!("env var {} not set", key_env))?
                    .trim()
                    .to_string();
                if key.is_empty() {
                    return Err(format!("env var {} is empty", key_env));
                }
                let secret = std::env::var(secret_env)
                    .map_err(|_| format!("env var {} not set", secret_env))?
                    .trim()
                    .to_string();
                if secret.is_empty() {
                    return Err(format!("env var {} is empty", secret_env));
                }
                let passphrase = match passphrase_env {
                    Some(env) => match std::env::var(env) {
                        Ok(v) if !v.trim().is_empty() => Some(v.trim().to_string()),
                        _ => None,
                    },
                    None => None,
                };
                Ok((key, secret, passphrase))
            }
            Credentials::Inline { api_key, api_secret, passphrase } => {
                Ok((api_key.clone(), api_secret.clone(), passphrase.clone()))
            }
        }
    }
}

/// Per-account risk overrides. `None` ⇒ fall back to global `BtcConfig`.
#[derive(Debug, Clone, Default)]
pub struct RiskOverrides {
    pub risk_per_trade_pct: Option<f64>,
    pub max_positions: Option<u32>,
    pub daily_loss_limit_btc: Option<f64>,
    pub max_consecutive_losses: Option<u32>,
    pub take_profit_pct: Option<f64>,
    pub stop_loss_pct: Option<f64>,
    pub trailing_tp_pct: Option<f64>,
}

/// Account specification — describes one (exchange, credentials, risk) tuple.
#[derive(Debug, Clone)]
pub struct AccountSpec {
    pub id: String,
    pub label: String,
    pub exchange: ExchangeKind,
    pub credentials: Credentials,
    pub scanner_pairs: Vec<String>,
    pub telegram_chat_ids: Vec<i64>,
    pub risk: RiskOverrides,
    pub enabled: bool,
}

/// Build a single-account spec from legacy `BINANCE_API_KEY` / `BINANCE_API_SECRET`.
///
/// Returns `None` when the legacy env vars are absent (advisory-only mode, identical
/// to today's behavior). `scanner_pairs` defaults to whatever the env says, falling
/// back to the same default the existing `config.rs` uses.
pub fn legacy_default_spec(
    exchange_name: &str,
    scanner_pairs: Vec<String>,
) -> Option<AccountSpec> {
    let exchange = ExchangeKind::from_str(exchange_name).unwrap_or(ExchangeKind::Binance);

    let key_env = "BINANCE_API_KEY".to_string();
    let secret_env = "BINANCE_API_SECRET".to_string();

    let key_resolved = std::env::var(&key_env).ok().filter(|v| !v.trim().is_empty()).is_some()
        || std::env::var("EXCHANGE_API_KEY").ok().filter(|v| !v.trim().is_empty()).is_some();
    let secret_resolved = std::env::var(&secret_env).ok().filter(|v| !v.trim().is_empty()).is_some()
        || std::env::var("EXCHANGE_API_SECRET").ok().filter(|v| !v.trim().is_empty()).is_some();

    if !key_resolved || !secret_resolved {
        return None;
    }

    Some(AccountSpec {
        id: "default".to_string(),
        label: "Default Binance".to_string(),
        exchange,
        credentials: Credentials::EnvKeySecret {
            key_env,
            secret_env,
            passphrase_env: None,
        },
        scanner_pairs,
        telegram_chat_ids: Vec::new(),
        risk: RiskOverrides::default(),
        enabled: true,
    })
}

/// Loader priority (Fase 0: legacy only — Fase 1 will add BTC_ACCOUNTS_JSON/LIST).
///
/// Returns 0 or 1 spec today. Empty vec = advisory-only (no credentials configured),
/// which is exactly the behavior the pre-Fase-0 main.rs has.
pub fn load_account_specs(
    exchange_name: &str,
    scanner_pairs: Vec<String>,
) -> Vec<AccountSpec> {
    match legacy_default_spec(exchange_name, scanner_pairs) {
        Some(spec) => vec![spec],
        None => Vec::new(),
    }
}

/// Validate a list of specs. Returns `Err` with a human message on the first problem.
pub fn validate(specs: &[AccountSpec]) -> Result<(), String> {
    let mut seen: HashSet<String> = HashSet::new();
    for s in specs {
        if s.id.is_empty() {
            return Err("account id is empty".into());
        }
        if !seen.insert(s.id.clone()) {
            return Err(format!("duplicate account id: {}", s.id));
        }
        if s.credentials.resolve().is_err() && s.enabled {
            return Err(format!(
                "account {} enabled but credentials unresolved (check env vars)",
                s.id
            ));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exchange_kind_roundtrip() {
        assert_eq!(ExchangeKind::from_str("binance"), Some(ExchangeKind::Binance));
        assert_eq!(ExchangeKind::from_str("OKX"), Some(ExchangeKind::Okx));
        assert_eq!(ExchangeKind::from_str("kraken"), None);
        assert_eq!(ExchangeKind::Binance.as_str(), "binance");
    }

    #[test]
    fn legacy_default_returns_none_when_no_env() {
        // Clear env to guarantee a clean state.
        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("EXCHANGE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
        std::env::remove_var("EXCHANGE_API_SECRET");
        let spec = legacy_default_spec("binance", vec!["ETHBTC".into()]);
        assert!(spec.is_none());
    }

    #[test]
    fn legacy_default_returns_spec_when_env_set() {
        std::env::set_var("BINANCE_API_KEY", "test_key");
        std::env::set_var("BINANCE_API_SECRET", "test_secret");
        let spec = legacy_default_spec("binance", vec!["ETHBTC".into()]).expect("spec");
        assert_eq!(spec.id, "default");
        assert_eq!(spec.exchange, ExchangeKind::Binance);
        assert!(spec.enabled);
        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
    }

    #[test]
    fn validate_rejects_duplicate_ids() {
        let specs = vec![
            AccountSpec {
                id: "a".into(),
                label: "A".into(),
                exchange: ExchangeKind::Binance,
                credentials: Credentials::Inline {
                    api_key: "k".into(),
                    api_secret: "s".into(),
                    passphrase: None,
                },
                scanner_pairs: vec![],
                telegram_chat_ids: vec![],
                risk: RiskOverrides::default(),
                enabled: false,
            },
            AccountSpec {
                id: "a".into(),
                label: "A2".into(),
                exchange: ExchangeKind::Binance,
                credentials: Credentials::Inline {
                    api_key: "k".into(),
                    api_secret: "s".into(),
                    passphrase: None,
                },
                scanner_pairs: vec![],
                telegram_chat_ids: vec![],
                risk: RiskOverrides::default(),
                enabled: false,
            },
        ];
        assert!(validate(&specs).is_err());
    }
}
