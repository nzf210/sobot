//! Multi-exchange dispatcher (Fase 0).
//!
//! Fase 0 scope: a thin wrapper around a single `Arc<dyn ExchangeClient>`,
//! preserving the exact pre-refactor runtime. Fase 1+ will populate the
//! inner `HashMap` with one entry per `AccountSpec` and route by `AccountKey`.

use std::collections::HashMap;
use std::sync::Arc;

use crate::account_spec::{AccountSpec, ExchangeKind};
use crate::binance::BinanceClient;
use crate::exchange::ExchangeClient;
use crate::okx::OkxClient;

#[derive(Debug, Clone, Hash, PartialEq, Eq)]
pub struct AccountKey {
    pub exchange: ExchangeKind,
    pub account_id: String,
}

impl AccountKey {
    pub fn from_spec(spec: &AccountSpec) -> Self {
        Self {
            exchange: spec.exchange,
            account_id: spec.id.clone(),
        }
    }
}

#[derive(Clone)]
pub struct AccountSummary {
    pub key: AccountKey,
    pub label: String,
    pub exchange: String,
    pub api_key_display: String,
    pub enabled: bool,
}

#[derive(Clone)]
pub struct MultiExchangeClient {
    accounts: HashMap<AccountKey, Arc<dyn ExchangeClient>>,
    default_key: Option<AccountKey>,
}

impl MultiExchangeClient {
    /// Build from validated account specs. Each spec's credentials are resolved;
    /// accounts that fail to resolve or are explicitly disabled are still kept
    /// in the map (so `/btc_accounts` can list them) but `default_key` only
    /// points to an enabled+resolved account.
    pub fn from_specs(specs: &[AccountSpec]) -> Self {
        let mut accounts: HashMap<AccountKey, Arc<dyn ExchangeClient>> = HashMap::new();
        let mut default_key: Option<AccountKey> = None;

        for spec in specs {
            let key = AccountKey::from_spec(spec);
            match build_client_for_spec(spec) {
                Ok(client) => {
                    if default_key.is_none() {
                        default_key = Some(key.clone());
                    }
                    accounts.insert(key, client);
                }
                Err(e) => {
                    tracing::warn!(
                        "MultiExchangeClient: skipping account {} — {}",
                        spec.id, e
                    );
                }
            }
        }

        Self { accounts, default_key }
    }

    /// Returns true if no accounts are configured (advisory-only mode).
    pub fn is_empty(&self) -> bool {
        self.accounts.is_empty()
    }

    /// The default `Arc<dyn ExchangeClient>`. Returns `None` if no account loaded.
    /// Used by existing call sites that previously held `Option<Arc<dyn ExchangeClient>>`.
    pub fn default(&self) -> Option<Arc<dyn ExchangeClient>> {
        self.default_key
            .as_ref()
            .and_then(|k| self.accounts.get(k).cloned())
    }

    /// Lookup a specific account by `(exchange, account_id)`.
    pub fn for_account(&self, key: &AccountKey) -> Option<Arc<dyn ExchangeClient>> {
        self.accounts.get(key).cloned()
    }

    /// Return every `(AccountKey, client)` pair that shares the given
    /// `account_id`. With a single Binance spec the vec has one entry;
    /// with `EXCHANGE_NAME=both` or a per-account JSON declaring two
    /// exchanges under one id, the vec has 2+ entries — that's the
    /// "1 account, 2 exchanges" lookup the bot uses to render per-binding
    /// status blocks.
    pub fn for_account_id(&self, account_id: &str) -> Vec<(AccountKey, Arc<dyn ExchangeClient>)> {
        self.accounts
            .iter()
            .filter(|(k, _)| k.account_id == account_id)
            .map(|(k, c)| (k.clone(), c.clone()))
            .collect()
    }

    /// List account summaries for `/btc/accounts` (Fase 1 will expand this with status).
    pub fn list(&self) -> Vec<AccountSummary> {
        self.accounts
            .iter()
            .map(|(k, client)| AccountSummary {
                key: k.clone(),
                label: k.account_id.clone(),
                exchange: k.exchange.as_str().to_string(),
                api_key_display: client.api_key_display(),
                enabled: Some(k) == self.default_key.as_ref() || self.default_key.is_none(),
            })
            .collect()
    }
}

fn build_client_for_spec(spec: &AccountSpec) -> anyhow::Result<Arc<dyn ExchangeClient>> {
    let (api_key, api_secret, passphrase) = spec
        .credentials
        .resolve()
        .map_err(|e| anyhow::anyhow!(e))?;

    match spec.exchange {
        ExchangeKind::Binance => {
            let base_url = std::env::var("EXCHANGE_BASE_URL")
                .ok()
                .filter(|v| !v.trim().is_empty());
            let client = BinanceClient::new(api_key, api_secret, base_url);
            tracing::info!(
                "Binance client initialized (account={}, api_key={})",
                spec.id,
                client.api_key_display()
            );
            Ok(Arc::new(client) as Arc<dyn ExchangeClient>)
        }
        ExchangeKind::Okx => {
            let base_url = std::env::var("EXCHANGE_BASE_URL")
                .ok()
                .filter(|v| !v.trim().is_empty());
            let passphrase = passphrase.unwrap_or_default();
            if passphrase.is_empty() {
                return Err(anyhow::anyhow!(
                    "OKX account {} requires a passphrase (set the passphrase env var for this account)",
                    spec.id
                ));
            }
            let client = OkxClient::new(api_key, api_secret, passphrase, base_url);
            tracing::info!(
                "OKX client initialized (account={}, api_key={})",
                spec.id,
                client.api_key_display_pub()
            );
            Ok(Arc::new(client) as Arc<dyn ExchangeClient>)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::account_spec::{Credentials, RiskOverrides};

    #[test]
    fn empty_dispatcher_reports_empty() {
        let m = MultiExchangeClient::from_specs(&[]);
        assert!(m.is_empty());
        assert!(m.default().is_none());
        assert!(m.list().is_empty());
    }

    #[test]
    fn dispatcher_with_one_disabled_account_is_empty() {
        // Even an enabled=false spec with unresolved creds is skipped at build.
        let spec = AccountSpec {
            id: "x".into(),
            label: "X".into(),
            exchange: ExchangeKind::Binance,
            credentials: Credentials::EnvKeySecret {
                key_env: "NONEXISTENT_KEY".into(),
                secret_env: "NONEXISTENT_SECRET".into(),
                passphrase_env: None,
            },
            scanner_pairs: vec![],
            telegram_chat_ids: vec![],
            risk: RiskOverrides::default(),
            enabled: false,
        };
        let m = MultiExchangeClient::from_specs(&[spec]);
        // credentials fail to resolve, so account is dropped from the map.
        assert!(m.is_empty());
    }

    #[test]
    fn dispatcher_routes_okx_account() {
        // Set up OKX env vars so Credentials::EnvKeySecret resolves.
        std::env::set_var("OKX_TEST_KEY", "okx_key_abcdef1234");
        std::env::set_var("OKX_TEST_SECRET", "okx_secret");
        std::env::set_var("OKX_TEST_PASSPHRASE", "okx_pass");

        let spec = AccountSpec {
            id: "okx_main".into(),
            label: "OKX Main".into(),
            exchange: ExchangeKind::Okx,
            credentials: Credentials::EnvKeySecret {
                key_env: "OKX_TEST_KEY".into(),
                secret_env: "OKX_TEST_SECRET".into(),
                passphrase_env: Some("OKX_TEST_PASSPHRASE".into()),
            },
            scanner_pairs: vec!["SOLBTC".into()],
            telegram_chat_ids: vec![],
            risk: RiskOverrides::default(),
            enabled: true,
        };

        let m = MultiExchangeClient::from_specs(&[spec.clone()]);
        assert!(!m.is_empty(), "dispatcher should hold the OKX account");
        let key = AccountKey::from_spec(&spec);
        let client = m.for_account(&key).expect("client");
        assert_eq!(client.exchange_name(), "OKX");
        assert!(client.api_key_display().contains("okx"));

        std::env::remove_var("OKX_TEST_KEY");
        std::env::remove_var("OKX_TEST_SECRET");
        std::env::remove_var("OKX_TEST_PASSPHRASE");
    }

    #[test]
    fn for_account_id_returns_all_exchanges_for_id() {
        // 1 account, 2 exchanges — Binance + OKX both under id="main".
        std::env::set_var("BINANCE_TEST_KEY", "binance_key");
        std::env::set_var("BINANCE_TEST_SECRET", "binance_secret");
        std::env::set_var("OKX_TEST_KEY", "okx_key");
        std::env::set_var("OKX_TEST_SECRET", "okx_secret");
        std::env::set_var("OKX_TEST_PASSPHRASE", "okx_pass");

        let binance_spec = AccountSpec {
            id: "main".into(),
            label: "B".into(),
            exchange: ExchangeKind::Binance,
            credentials: Credentials::EnvKeySecret {
                key_env: "BINANCE_TEST_KEY".into(),
                secret_env: "BINANCE_TEST_SECRET".into(),
                passphrase_env: None,
            },
            scanner_pairs: vec!["SOLBTC".into()],
            telegram_chat_ids: vec![],
            risk: RiskOverrides::default(),
            enabled: true,
        };
        let okx_spec = AccountSpec {
            id: "main".into(),
            label: "O".into(),
            exchange: ExchangeKind::Okx,
            credentials: Credentials::EnvKeySecret {
                key_env: "OKX_TEST_KEY".into(),
                secret_env: "OKX_TEST_SECRET".into(),
                passphrase_env: Some("OKX_TEST_PASSPHRASE".into()),
            },
            scanner_pairs: vec!["SOLBTC".into()],
            telegram_chat_ids: vec![],
            risk: RiskOverrides::default(),
            enabled: true,
        };

        let m = MultiExchangeClient::from_specs(&[binance_spec, okx_spec]);
        let main_bindings = m.for_account_id("main");
        assert_eq!(main_bindings.len(), 2, "id=main should have 2 exchange bindings");
        let names: Vec<&str> = main_bindings.iter().map(|(k, _)| k.exchange.as_str()).collect();
        assert!(names.contains(&"binance"));
        assert!(names.contains(&"okx"));

        // Default key is the first binding — Binance in this case.
        let default = m.default().expect("default");
        assert_eq!(default.exchange_name(), "Binance");

        std::env::remove_var("BINANCE_TEST_KEY");
        std::env::remove_var("BINANCE_TEST_SECRET");
        std::env::remove_var("OKX_TEST_KEY");
        std::env::remove_var("OKX_TEST_SECRET");
        std::env::remove_var("OKX_TEST_PASSPHRASE");
    }
}
