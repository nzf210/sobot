#![allow(dead_code)]
//! Multi-account & multi-CEX account specifications (Fase 0-3).
//!
//! Fase 0 introduces the data model and loader only — no runtime behavior
//! change. Existing `BINANCE_API_KEY`/`BINANCE_API_SECRET` env vars continue
//! to drive a single `default` Binance account, identical to today.
//!
//! Fase 3 (this file's current scope) extends the loader to:
//! 1. **Per-account JSON config**: `data_dir/accounts/{id}/accounts.json`
//!    (or `data_dir/btc-accounts.json` for the legacy `default` account).
//!    A single JSON file may declare N accounts; an account with id=`main`
//!    may declare an `exchanges[]` array of `{binance, okx}` entries — that
//!    is the user-facing "1 account, 2 exchanges" mechanism.
//! 2. **`EXCHANGE_NAME=both` / `EXCHANGE_NAME=binance,okx` env-var fan-out**:
//!    legacy single-exchange path that produces one or more `default`
//!    specs depending on what credentials are configured. Used when no
//!    accounts.json is present (so the env-var path still works for the
//!    dev / docker-compose workflow).

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
#[derive(Debug, Clone, Default, serde::Deserialize)]
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

/// Build a single-account spec from legacy env vars.
///
/// - Binance: `BINANCE_API_KEY` + `BINANCE_API_SECRET`
/// - OKX: `OKX_API_KEY` + `OKX_API_SECRET` + `OKX_API_PASSPHRASE`
///
/// Returns `None` when the legacy env vars are absent (advisory-only mode, identical
/// to today's behavior). `scanner_pairs` defaults to whatever the env says, falling
/// back to the same default the existing `config.rs` uses.
pub fn legacy_default_spec(
    exchange_name: &str,
    scanner_pairs: Vec<String>,
) -> Option<AccountSpec> {
    let exchange = ExchangeKind::from_str(exchange_name).unwrap_or(ExchangeKind::Binance);

    let (key_env, secret_env, passphrase_env) = match exchange {
        ExchangeKind::Binance => (
            "BINANCE_API_KEY".to_string(),
            "BINANCE_API_SECRET".to_string(),
            None,
        ),
        ExchangeKind::Okx => (
            "OKX_API_KEY".to_string(),
            "OKX_API_SECRET".to_string(),
            Some("OKX_API_PASSPHRASE".to_string()),
        ),
    };

    // Resolve the actually-present env var name for each credential. The
    // probe below also accepts the legacy `EXCHANGE_API_KEY` /
    // `EXCHANGE_API_SECRET` aliases (used by single-exchange configs that
    // pre-date the per-exchange naming), but the var we *store* in the
    // `Credentials` struct must be the one that's actually set, otherwise
    // `Credentials::resolve()` later will fail to find it and the
    // dispatcher drops the account silently.
    let resolved_key_env: String = if std::env::var(&key_env).ok().filter(|v| !v.trim().is_empty()).is_some() {
        key_env
    } else if std::env::var("EXCHANGE_API_KEY").ok().filter(|v| !v.trim().is_empty()).is_some() {
        "EXCHANGE_API_KEY".to_string()
    } else {
        return None;
    };
    let resolved_secret_env: String = if std::env::var(&secret_env).ok().filter(|v| !v.trim().is_empty()).is_some() {
        secret_env
    } else if std::env::var("EXCHANGE_API_SECRET").ok().filter(|v| !v.trim().is_empty()).is_some() {
        "EXCHANGE_API_SECRET".to_string()
    } else {
        return None;
    };

    // For OKX, also require the passphrase; otherwise the dispatcher will
    // refuse to build the client. Fail fast here so the user sees a clear
    // message at startup.
    if let Some(ref pp_env) = passphrase_env {
        if std::env::var(pp_env).ok().filter(|v| !v.trim().is_empty()).is_none() {
            return None;
        }
    }

    Some(AccountSpec {
        id: "default".to_string(),
        label: match exchange {
            ExchangeKind::Binance => "Default Binance".to_string(),
            ExchangeKind::Okx => "Default OKX".to_string(),
        },
        exchange,
        credentials: Credentials::EnvKeySecret {
            key_env: resolved_key_env,
            secret_env: resolved_secret_env,
            passphrase_env,
        },
        scanner_pairs,
        telegram_chat_ids: Vec::new(),
        risk: RiskOverrides::default(),
        enabled: true,
    })
}

/// Loader priority (Fase 3):
///   1. `BTC_ACCOUNTS_JSON` env var (raw JSON string) — explicit override
///   2. `data_dir/btc-accounts.json` (or `data_dir/accounts/{id}/accounts.json`)
///   3. Legacy env-var fan-out via `legacy_default_specs` (handles
///      `EXCHANGE_NAME=both` / `binance,okx` / `binance` / unset)
///
/// Returns 0+ specs. Empty vec = advisory-only (no credentials configured),
/// which is exactly the behavior the pre-Fase-0 main.rs has.
pub fn load_account_specs(
    exchange_name: &str,
    scanner_pairs: Vec<String>,
) -> Vec<AccountSpec> {
    // 1. Explicit env-var JSON
    if let Ok(json) = std::env::var("BTC_ACCOUNTS_JSON") {
        if !json.trim().is_empty() {
            match load_account_specs_from_json(&json) {
                Ok(specs) => return specs,
                Err(e) => {
                    tracing::error!("BTC_ACCOUNTS_JSON parse failed: {}", e);
                }
            }
        }
    }
    // 2. Per-account JSON file. Default-account (id=default) uses the flat
    //    file; named ids look under accounts/{id}/accounts.json.
    if let Some(default_json) = std::env::var("DATA_BTC_DIR")
        .ok()
        .or_else(|| std::env::var("DATA_DIR").ok())
    {
        let flat = std::path::Path::new(&default_json).join("btc-accounts.json");
        if flat.exists() {
            if let Ok(s) = std::fs::read_to_string(&flat) {
                match load_account_specs_from_json(&s) {
                    Ok(specs) => return specs,
                    Err(e) => {
                        tracing::error!("btc-accounts.json parse failed: {}", e);
                    }
                }
            }
        }
        let accounts_dir = std::path::Path::new(&default_json).join("accounts");
        if accounts_dir.is_dir() {
            if let Ok(entries) = std::fs::read_dir(&accounts_dir) {
                for entry in entries.flatten() {
                    let path = entry.path().join("accounts.json");
                    if path.is_file() {
                        if let Ok(s) = std::fs::read_to_string(&path) {
                            match load_account_specs_from_json(&s) {
                                Ok(specs) => {
                                    // If the per-account file declares its id
                                    // as "default" it's a flat-layout user; we
                                    // still want their specs loaded as-is.
                                    return specs;
                                }
                                Err(e) => {
                                    tracing::error!("{} parse failed: {}", path.display(), e);
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    // 3. Legacy env-var fan-out
    legacy_default_specs(exchange_name, scanner_pairs)
}

/// Fan out `EXCHANGE_NAME` into one or more `default` specs.
///
/// - `"binance"` (or unset) → 0 or 1 spec
/// - `"okx"` → 0 or 1 spec
/// - `"both"` → 0, 1, or 2 specs (one per exchange whose env vars are set)
/// - `"binance,okx"` (or any CSV) → fan out into N specs
///
/// Each spec gets the same `id="default"` and the same `scanner_pairs`.
/// Credentials are read from the env vars the corresponding `legacy_default_spec`
/// understands. Specs that fail to resolve are dropped silently.
pub fn legacy_default_specs(
    exchange_name: &str,
    scanner_pairs: Vec<String>,
) -> Vec<AccountSpec> {
    let lowered = exchange_name.trim().to_lowercase();
    let names: Vec<&str> = if lowered == "both" {
        vec!["binance", "okx"]
    } else {
        lowered
            .split(',')
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .collect()
    };
    if names.is_empty() {
        return Vec::new();
    }
    names
        .iter()
        .filter_map(|n| legacy_default_spec(n, scanner_pairs.clone()))
        .collect()
}

/// JSON loader. The on-disk schema is intentionally close to the runtime
/// `AccountSpec` shape, with a small `exchanges[]` sub-array to express
/// "1 account, N exchanges".
///
/// On-disk schema (Fase 3):
/// ```json
/// {
///   "accounts": [
///     {
///       "id": "main",
///       "label": "Main Treasury",
///       "telegram_chat_ids": [123456789],
///       "exchanges": [
///         { "kind": "binance", "api_key": "...", "api_secret": "...",
///           "scanner_pairs": ["SOLBTC"], "enabled": true, "risk": {} },
///         { "kind": "okx", "api_key": "...", "api_secret": "...",
///           "passphrase": "...", "scanner_pairs": ["ETHBTC"],
///           "enabled": true, "risk": {} }
///       ]
///     }
///   ]
/// }
/// ```
///
/// Returns one `AccountSpec` per `(id, exchange)` pair. Two exchanges under
/// the same id become two specs that share the id but differ in `exchange`.
pub fn load_account_specs_from_json(json_str: &str) -> Result<Vec<AccountSpec>, String> {
    let raw: AccountsConfigRaw = serde_json::from_str(json_str)
        .map_err(|e| format!("accounts JSON: {}", e))?;
    let mut specs = Vec::new();
    for acct in raw.accounts {
        let id = acct.id.trim().to_string();
        if id.is_empty() {
            return Err("account id is empty in accounts JSON".into());
        }
        let label = acct.label.unwrap_or_else(|| id.clone());
        let chat_ids = acct.telegram_chat_ids.unwrap_or_default();
        for ex in acct.exchanges {
            let exchange = match ex.kind.as_deref() {
                Some("binance") => ExchangeKind::Binance,
                Some("okx") => ExchangeKind::Okx,
                Some(other) => return Err(format!("unknown exchange kind '{}' for account {}", other, id)),
                None => return Err(format!("missing 'kind' in exchange for account {}", id)),
            };
            let api_key = ex.api_key.ok_or_else(|| format!("missing api_key for {}/{}", id, ex.kind.as_deref().unwrap_or("?")))?;
            let api_secret = ex.api_secret.ok_or_else(|| format!("missing api_secret for {}/{}", id, ex.kind.as_deref().unwrap_or("?")))?;
            if api_key.trim().is_empty() || api_secret.trim().is_empty() {
                return Err(format!("empty credentials for {}/{}", id, ex.kind.as_deref().unwrap_or("?")));
            }
            let passphrase = ex.passphrase.filter(|p| !p.is_empty());
            if matches!(exchange, ExchangeKind::Okx) && passphrase.is_none() {
                return Err(format!("OKX account {} requires a passphrase", id));
            }
            let scanner_pairs = ex.scanner_pairs.unwrap_or_default();
            let enabled = ex.enabled.unwrap_or(true);
            let risk = ex.risk.unwrap_or_default();
            specs.push(AccountSpec {
                id: id.clone(),
                label: format!("{} ({})", label, exchange.as_str()),
                exchange,
                credentials: Credentials::Inline {
                    api_key,
                    api_secret,
                    passphrase,
                },
                scanner_pairs,
                telegram_chat_ids: chat_ids.clone(),
                risk,
                enabled,
            });
        }
    }
    Ok(specs)
}

/// Validate a list of specs. Returns `Err` with a human message on the first
/// problem. Fase 3: same `id` is allowed as long as the `(id, exchange)` pair
/// is unique — that's how "1 account, 2 exchanges" is expressed.
pub fn validate(specs: &[AccountSpec]) -> Result<(), String> {
    let mut seen: HashSet<(String, ExchangeKind)> = HashSet::new();
    for s in specs {
        if s.id.is_empty() {
            return Err("account id is empty".into());
        }
        if !seen.insert((s.id.clone(), s.exchange)) {
            return Err(format!(
                "duplicate (id, exchange) pair: ({}, {})",
                s.id, s.exchange.as_str()
            ));
        }
        if s.credentials.resolve().is_err() && s.enabled {
            return Err(format!(
                "account {} on {} enabled but credentials unresolved",
                s.id, s.exchange.as_str()
            ));
        }
    }
    Ok(())
}

// ── Raw JSON shapes (kept private — deserialization-only) ─────────────────────

#[derive(Debug, Default, serde::Deserialize)]
struct AccountsConfigRaw {
    #[serde(default)]
    accounts: Vec<AccountEntryRaw>,
}

#[derive(Debug, Default, serde::Deserialize)]
struct AccountEntryRaw {
    id: String,
    #[serde(default)]
    label: Option<String>,
    #[serde(default)]
    telegram_chat_ids: Option<Vec<i64>>,
    #[serde(default)]
    exchanges: Vec<ExchangeEntryRaw>,
}

#[derive(Debug, Default, serde::Deserialize)]
struct ExchangeEntryRaw {
    #[serde(default)]
    kind: Option<String>,
    #[serde(default)]
    api_key: Option<String>,
    #[serde(default)]
    api_secret: Option<String>,
    #[serde(default)]
    passphrase: Option<String>,
    #[serde(default)]
    scanner_pairs: Option<Vec<String>>,
    #[serde(default)]
    enabled: Option<bool>,
    #[serde(default)]
    risk: Option<RiskOverrides>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    // Env-var-mutating tests must run serially. Multiple tests in this module
    // call std::env::set_var/remove_var on the SAME env var names (e.g.
    // BINANCE_API_KEY), and parallel execution causes cross-test pollution:
    // one test sets/clears while another reads. The Mutex serializes them
    // even when cargo test runs with --test-threads>1.
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    #[test]
    fn exchange_kind_roundtrip() {
        assert_eq!(ExchangeKind::from_str("binance"), Some(ExchangeKind::Binance));
        assert_eq!(ExchangeKind::from_str("OKX"), Some(ExchangeKind::Okx));
        assert_eq!(ExchangeKind::from_str("kraken"), None);
        assert_eq!(ExchangeKind::Binance.as_str(), "binance");
    }

    #[test]
    fn legacy_default_returns_none_when_no_env() {
        let _g = ENV_LOCK.lock().unwrap();
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
        let _g = ENV_LOCK.lock().unwrap();
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

    #[test]
    fn legacy_default_okx_spec_requires_passphrase_env() {
        let _g = ENV_LOCK.lock().unwrap();
        // Make sure we start clean.
        std::env::remove_var("OKX_API_KEY");
        std::env::remove_var("OKX_API_SECRET");
        std::env::remove_var("OKX_API_PASSPHRASE");

        // Without any OKX env vars: returns None.
        assert!(legacy_default_spec("okx", vec!["SOLBTC".into()]).is_none());

        // With key + secret but no passphrase: still None (passphrase is
        // mandatory for OKX — without it the dispatcher would fail anyway).
        std::env::set_var("OKX_API_KEY", "test_key");
        std::env::set_var("OKX_API_SECRET", "test_secret");
        assert!(legacy_default_spec("okx", vec!["SOLBTC".into()]).is_none());

        // With all three: returns a spec with ExchangeKind::Okx and the
        // passphrase env recorded.
        std::env::set_var("OKX_API_PASSPHRASE", "test_pass");
        let spec = legacy_default_spec("okx", vec!["SOLBTC".into()]).expect("spec");
        assert_eq!(spec.id, "default");
        assert_eq!(spec.exchange, ExchangeKind::Okx);
        match spec.credentials {
            Credentials::EnvKeySecret { key_env, secret_env, passphrase_env } => {
                assert_eq!(key_env, "OKX_API_KEY");
                assert_eq!(secret_env, "OKX_API_SECRET");
                assert_eq!(passphrase_env.as_deref(), Some("OKX_API_PASSPHRASE"));
            }
            _ => panic!("expected EnvKeySecret"),
        }

        // Cleanup.
        std::env::remove_var("OKX_API_KEY");
        std::env::remove_var("OKX_API_SECRET");
        std::env::remove_var("OKX_API_PASSPHRASE");
    }

    // ── Fase 3: legacy fan-out + JSON loader tests ──────────────────────────

    #[test]
    fn legacy_default_specs_both_returns_two_specs() {
        let _g = ENV_LOCK.lock().unwrap();
        // Set all env vars so both exchanges resolve.
        std::env::set_var("BINANCE_API_KEY", "test_binance_key");
        std::env::set_var("BINANCE_API_SECRET", "test_binance_secret");
        std::env::set_var("OKX_API_KEY", "test_okx_key");
        std::env::set_var("OKX_API_SECRET", "test_okx_secret");
        std::env::set_var("OKX_API_PASSPHRASE", "test_okx_pass");

        let specs = legacy_default_specs("both", vec!["SOLBTC".into()]);
        assert_eq!(specs.len(), 2, "EXCHANGE_NAME=both with both env vars set should produce 2 specs");
        assert_eq!(specs[0].id, "default");
        assert_eq!(specs[0].exchange, ExchangeKind::Binance);
        assert_eq!(specs[1].id, "default");
        assert_eq!(specs[1].exchange, ExchangeKind::Okx);

        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
        std::env::remove_var("OKX_API_KEY");
        std::env::remove_var("OKX_API_SECRET");
        std::env::remove_var("OKX_API_PASSPHRASE");
    }

    #[test]
    fn legacy_default_specs_csv_form() {
        let _g = ENV_LOCK.lock().unwrap();
        std::env::set_var("BINANCE_API_KEY", "k");
        std::env::set_var("BINANCE_API_SECRET", "s");
        std::env::set_var("OKX_API_KEY", "k");
        std::env::set_var("OKX_API_SECRET", "s");
        std::env::set_var("OKX_API_PASSPHRASE", "p");

        let specs = legacy_default_specs("binance,okx", vec!["ETHBTC".into()]);
        assert_eq!(specs.len(), 2);
        assert_eq!(specs[0].exchange, ExchangeKind::Binance);
        assert_eq!(specs[1].exchange, ExchangeKind::Okx);

        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
        std::env::remove_var("OKX_API_KEY");
        std::env::remove_var("OKX_API_SECRET");
        std::env::remove_var("OKX_API_PASSPHRASE");
    }

    #[test]
    fn legacy_default_specs_only_resolves_configured_exchanges() {
        let _g = ENV_LOCK.lock().unwrap();
        // Only Binance env set, EXCHANGE_NAME=both → 1 spec (OKX silently dropped).
        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
        std::env::remove_var("OKX_API_KEY");
        std::env::remove_var("OKX_API_SECRET");
        std::env::remove_var("OKX_API_PASSPHRASE");

        std::env::set_var("BINANCE_API_KEY", "k");
        std::env::set_var("BINANCE_API_SECRET", "s");
        let specs = legacy_default_specs("both", vec![]);
        assert_eq!(specs.len(), 1);
        assert_eq!(specs[0].exchange, ExchangeKind::Binance);

        std::env::remove_var("BINANCE_API_KEY");
        std::env::remove_var("BINANCE_API_SECRET");
    }

    #[test]
    fn load_account_specs_from_json_two_exchanges_one_id() {
        let json = r#"{
            "accounts": [
                {
                    "id": "main",
                    "label": "Main",
                    "telegram_chat_ids": [12345],
                    "exchanges": [
                        { "kind": "binance", "api_key": "bk", "api_secret": "bs",
                          "scanner_pairs": ["SOLBTC"], "enabled": true },
                        { "kind": "okx", "api_key": "ok", "api_secret": "os",
                          "passphrase": "op", "scanner_pairs": ["ETHBTC"], "enabled": true }
                    ]
                }
            ]
        }"#;
        let specs = load_account_specs_from_json(json).expect("parse");
        assert_eq!(specs.len(), 2);
        assert_eq!(specs[0].id, "main");
        assert_eq!(specs[0].exchange, ExchangeKind::Binance);
        assert_eq!(specs[0].telegram_chat_ids, vec![12345]);
        assert_eq!(specs[0].scanner_pairs, vec!["SOLBTC".to_string()]);
        assert_eq!(specs[1].id, "main");
        assert_eq!(specs[1].exchange, ExchangeKind::Okx);
        assert_eq!(specs[1].scanner_pairs, vec!["ETHBTC".to_string()]);
        match &specs[1].credentials {
            Credentials::Inline { passphrase, .. } => assert_eq!(passphrase.as_deref(), Some("op")),
            _ => panic!("expected Inline credentials"),
        }
    }

    #[test]
    fn load_account_specs_from_json_rejects_invalid_schema() {
        // Missing api_key
        let bad1 = r#"{ "accounts": [ { "id": "x", "exchanges": [
            { "kind": "binance", "api_secret": "s" } ] } ] }"#;
        assert!(load_account_specs_from_json(bad1).is_err());
        // Malformed JSON
        let bad2 = "{ not json";
        assert!(load_account_specs_from_json(bad2).is_err());
        // Unknown kind
        let bad3 = r#"{ "accounts": [ { "id": "x", "exchanges": [
            { "kind": "kraken", "api_key": "k", "api_secret": "s" } ] } ] }"#;
        assert!(load_account_specs_from_json(bad3).is_err());
        // OKX without passphrase
        let bad4 = r#"{ "accounts": [ { "id": "x", "exchanges": [
            { "kind": "okx", "api_key": "k", "api_secret": "s" } ] } ] }"#;
        assert!(load_account_specs_from_json(bad4).is_err());
    }

    #[test]
    fn load_account_specs_from_json_file_two_exchanges_one_id() {
        // End-to-end: write a JSON file to disk, then call load_account_specs
        // (the function main.rs uses) and verify the dispatcher-style routing
        // for "1 account, 2 exchanges" comes out the way the bot expects.
        let _g = ENV_LOCK.lock().unwrap();
        // Make sure BTC_ACCOUNTS_JSON isn't set so the per-file path is used.
        std::env::remove_var("BTC_ACCOUNTS_JSON");
        let tmp = "./data/test_load_specs_json_file";
        std::fs::create_dir_all(tmp).unwrap();
        let json_path = std::path::Path::new(tmp).join("btc-accounts.json");
        std::fs::write(&json_path, r#"{
            "accounts": [
                {
                    "id": "main",
                    "label": "Main",
                    "telegram_chat_ids": [],
                    "exchanges": [
                        { "kind": "binance", "api_key": "bk", "api_secret": "bs",
                          "scanner_pairs": ["SOLBTC"], "enabled": true },
                        { "kind": "okx", "api_key": "ok", "api_secret": "os",
                          "passphrase": "op", "scanner_pairs": ["ETHBTC"], "enabled": true }
                    ]
                }
            ]
        }"#).unwrap();
        // Set DATA_BTC_DIR to point at our temp dir so the loader finds the file.
        let prev = std::env::var("DATA_BTC_DIR").ok();
        std::env::set_var("DATA_BTC_DIR", tmp);
        let specs = load_account_specs("binance", vec![]);
        std::env::remove_var("DATA_BTC_DIR");
        if let Some(p) = prev { std::env::set_var("DATA_BTC_DIR", p); }
        std::fs::remove_dir_all(tmp).ok();

        assert_eq!(specs.len(), 2, "JSON file with 2 exchanges under id=main should produce 2 specs");
        assert_eq!(specs[0].id, "main");
        assert_eq!(specs[0].exchange, ExchangeKind::Binance);
        assert_eq!(specs[1].id, "main");
        assert_eq!(specs[1].exchange, ExchangeKind::Okx);
        // Both must validate cleanly.
        assert!(validate(&specs).is_ok());
    }

    #[test]
    fn validate_allows_same_id_different_exchanges() {
        // 1 account, 2 exchanges — should validate cleanly.
        let specs = vec![
            AccountSpec {
                id: "main".into(),
                label: "M".into(),
                exchange: ExchangeKind::Binance,
                credentials: Credentials::Inline { api_key: "k".into(), api_secret: "s".into(), passphrase: None },
                scanner_pairs: vec![], telegram_chat_ids: vec![], risk: RiskOverrides::default(),
                enabled: true,
            },
            AccountSpec {
                id: "main".into(),
                label: "M".into(),
                exchange: ExchangeKind::Okx,
                credentials: Credentials::Inline { api_key: "k".into(), api_secret: "s".into(), passphrase: Some("p".into()) },
                scanner_pairs: vec![], telegram_chat_ids: vec![], risk: RiskOverrides::default(),
                enabled: true,
            },
        ];
        assert!(validate(&specs).is_ok());

        // Same id AND same exchange → must fail.
        let dup = vec![specs[0].clone(), specs[0].clone()];
        assert!(validate(&dup).is_err());
    }
}
