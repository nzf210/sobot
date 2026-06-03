use std::fs;
use std::path::PathBuf;
use std::sync::RwLock;

use crate::account_spec::ExchangeKind;
use crate::models::*;

/// Per-account, per-exchange state store (Fase 3).
///
/// Layout:
///
/// | `account_id`         | `exchange`     | Filesystem path                             |
/// |----------------------|----------------|---------------------------------------------|
/// | `None`               | `None`         | `data_dir/...`                              |
/// | `Some("default")`    | `None`         | `data_dir/...` (legacy single-account)      |
/// | `Some("default")`    | `Some(_)`      | `data_dir/...` (legacy compat — no subdir)  |
/// | `Some(other)`        | `None`         | `data_dir/accounts/{id}/...`                |
/// | `Some(other)`        | `Some(ex)`     | `data_dir/accounts/{id}/{ex}/...`           |
///
/// The legacy `default` account keeps the flat layout even when an exchange
/// is provided — this preserves byte-for-byte compatibility with Fase 2
/// single-account users. New named accounts created post-Fase 3 get the
/// layered subdir so two exchanges under the same id never collide on
/// `btc-treasury.json`.
pub struct MemoryStore {
    data_dir: PathBuf,
    account_dir: PathBuf,
    account_id: Option<String>,
    exchange_kind: Option<ExchangeKind>,
    lock: RwLock<()>,
}

impl MemoryStore {
    /// Build a store rooted at `data_dir` for the default (legacy) account.
    /// Files live directly in `data_dir` (e.g. `btc-treasury.json`).
    pub fn new(data_dir: &str) -> Self {
        Self::with_account(data_dir, None, None)
    }

    /// Build a store scoped to a specific account and exchange. See struct
    /// doc for the layout table.
    ///
    /// `account_id == Some("default")` is treated as the legacy flat layout
    /// regardless of `exchange`, so users running the default account under
    /// multiple exchanges (via `EXCHANGE_NAME=both`) keep their state in
    /// `data_dir/btc-treasury.json`. This is intentional: a Fase 2 user
    /// upgrading to Fase 3 with no per-account JSON file continues to see
    /// byte-identical behavior.
    pub fn with_account(
        data_dir: &str,
        account_id: Option<&str>,
        exchange: Option<ExchangeKind>,
    ) -> Self {
        let dir = PathBuf::from(data_dir);
        fs::create_dir_all(&dir).expect("Failed to create data directory");

        let is_legacy_default = matches!(account_id, None | Some("default"));
        let account_dir = match (account_id, exchange, is_legacy_default) {
            // Named id + explicit exchange → layered subdir (Fase 3 isolation)
            (Some(id), Some(ex), false) => {
                let sub = dir.join("accounts").join(id).join(ex.as_str());
                fs::create_dir_all(&sub).expect("Failed to create account data directory");
                sub
            }
            // Named id without exchange (Fase 2 upgrade path) → single subdir
            (Some(id), None, false) => {
                let sub = dir.join("accounts").join(id);
                fs::create_dir_all(&sub).expect("Failed to create account data directory");
                sub
            }
            // Default account (with or without exchange) → flat data_dir
            _ => dir.clone(),
        };

        let store = Self {
            data_dir: dir,
            account_dir,
            account_id: account_id.map(|s| s.to_string()),
            exchange_kind: exchange,
            lock: RwLock::new(()),
        };
        store.init_defaults();
        store
    }

    pub fn account_id(&self) -> Option<&str> {
        self.account_id.as_deref()
    }

    /// The exchange this store is scoped to, if any. `None` for the legacy
    /// default-account flat layout and for the rare named-account-upgrade
    /// case where the exchange was not provided.
    pub fn exchange(&self) -> Option<ExchangeKind> {
        self.exchange_kind
    }

    fn init_defaults(&self) {
        let defaults: Vec<(&str, &str)> = vec![
            ("btc-treasury.json", r#"{"current_btc":0,"previous_btc":0,"btc_growth_7d":0,"btc_growth_30d":0,"stable_value":0,"usdt_balance":0,"last_update":"","btc_treasury_vault":0,"compound_balance":0,"total_trades":0,"winning_trades":0,"losing_trades":0,"trading_paused_until":"","consecutive_losses":0}"#),
            ("btc-decision-log.json", "[]"),
            ("btc-config.json", r#"{"enabled":true,"llm_activation_threshold":0.85,"min_confidence":0.80,"max_exposure":0.50,"daily_loss_limit_btc":0.0005,"max_consecutive_losses":3,"safe_mode_volatility":9.0,"safe_mode_drawdown":0.05,"scanner_pairs":["BTCUSDT","SOLBTC","ETHBTC","BNBBTC","XRPBTC","ADABTC","LINKBTC","SUIBTC","AVAXBTC","DOGEBTC"],"take_profit_pct":5.5,"stop_loss_pct":-1.5,"trailing_tp_pct":3.0,"use_trailing":true,"max_positions":1,"risk_per_trade_pct":0.01,"initial_capital_usdt":50.0,"min_score_threshold":80.0,"compound_pct":0.50,"treasury_pct":0.50,"dry_run":true}"#),
            ("btc-positions.json", "[]"),
            ("btc-lessons.json", "[]"),
        ];

        // SKILL.md stays in the data_dir root (it's the bot's persona file
        // and is shared across all accounts). The account_dir defaults also
        // link to the same content via a relative path so a per-account
        // override can drop in later without changing the read path.
        let skill_path = self.data_dir.join("SKILL.md");
        if !skill_path.exists() {
            let skill_content = ["SKILL.md", "../SKILL.md", "/app/SKILL.md"]
                .iter()
                .find_map(|p| fs::read_to_string(p).ok())
                .unwrap_or_else(|| {
                    "# BTC Treasury Advisor (Spot)\n- Autonomous Binance spot scanner\n- Market regime detection\n- Risk assessment\n- LLM reasoning".into()
                });
            fs::write(&skill_path, skill_content).expect("Failed to write SKILL.md");
        }

        for (filename, content) in defaults {
            let path = self.account_dir.join(filename);
            if !path.exists() {
                fs::write(&path, content).expect("Failed to write default file");
            }
        }
    }

    fn read_json<T: serde::de::DeserializeOwned>(&self, filename: &str, default: T) -> T {
        let _guard = self.lock.read().unwrap();
        let path = self.account_dir.join(filename);
        fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or(default)
    }

    /// Atomic JSON write: serialize → write to `<file>.tmp` → rename to target.
    /// On Linux, `fs::rename` on the same filesystem is atomic, so a crash
    /// mid-write can never leave a partially-written JSON file (which would
    /// otherwise lose the entire position history on next boot).
    fn write_json<T: serde::Serialize>(&self, filename: &str, data: &T) {
        let _guard = self.lock.write().unwrap();
        let path = self.account_dir.join(filename);
        let tmp_path = self.account_dir.join(format!("{}.tmp", filename));
        let json = serde_json::to_string_pretty(data).expect("Failed to serialize");
        if let Err(e) = fs::write(&tmp_path, &json) {
            tracing::error!("memory: failed to write tmp {}: {}", tmp_path.display(), e);
            return;
        }
        if let Err(e) = fs::rename(&tmp_path, &path) {
            tracing::error!("memory: failed to rename {} → {}: {}", tmp_path.display(), path.display(), e);
            // Best-effort cleanup; do not panic — the original file is still intact.
            let _ = fs::remove_file(&tmp_path);
        }
    }

    pub fn get_treasury_state(&self) -> BtcTreasuryState {
        self.read_json("btc-treasury.json", BtcTreasuryState::default())
    }

    pub fn save_treasury_state(&self, mut state: BtcTreasuryState) {
        state.last_update = chrono::Utc::now().to_rfc3339();
        self.write_json("btc-treasury.json", &state);
    }

    /// Sync treasury state with live Binance Spot balances.
    ///
    /// Without this, `btc-treasury.json` starts at zero and only diverges
    /// further on each closed position — the bot would display "BTC
    /// Holdings: 0.0" forever even when the account actually holds BTC,
    /// and `RiskManager::assess` would see `usdt_balance = 0.0` and refuse
    /// to size any position.
    ///
    /// Strategy: adopt the live BTC and USDT balances as the new baseline,
    /// but preserve any profit-split fields (`btc_treasury_vault`,
    /// `compound_balance`, trade counters) so the per-trade split logic
    /// stays intact. The "live" BTC replaces both `current_btc` and
    /// `previous_btc` so growth calculations on the next 7d/30d window
    /// have a consistent starting point.
    pub fn sync_initial_balances(&self, live_btc: f64, live_usdt: f64) {
        let mut state = self.get_treasury_state();
        state.current_btc = live_btc;
        state.previous_btc = live_btc;
        state.usdt_balance = live_usdt;
        state.stable_value = live_usdt;
        self.save_treasury_state(state);
        tracing::info!(
            "Synced treasury with Binance balances: BTC={:.8} USDT={:.2}",
            live_btc, live_usdt
        );
    }

    /// Update `btc_growth_7d` / `btc_growth_30d` from the current
    /// `current_btc` vs `previous_btc`. Should be called once per close
    /// (after `update_treasury_on_close`) and once at startup
    /// (after `sync_initial_balances`) so `assess_risk` can read a
    /// real ratio instead of the always-zero default.
    ///
    /// `btc_growth_7d` is the ratio (current_btc - previous_btc) / previous_btc.
    /// Negative on a loss-anchored close is fine — that's the treasury delta.
    pub fn update_growth_ratios(&self) {
        let mut state = self.get_treasury_state();
        let prev = state.previous_btc;
        if prev > 0.0 {
            let ratio = (state.current_btc - prev) / prev;
            // 7d and 30d windows would normally use a rolling history; we
            // approximate by treating this as a same-window delta. Once
            // history persistence is added, swap in real 7d/30d snapshots.
            state.btc_growth_7d = ratio;
            state.btc_growth_30d = ratio;
        }
        self.save_treasury_state(state);
    }

    /// Re-sync treasury balance fields with live Binance Spot balances
    /// after a fill. Used by the position-monitor close path: by the time
    /// the close order returns, the live exchange balances are the
    /// source of truth (the local ledger's PnL calculation may differ
    /// from real fills due to slippage, partial fills, fees). This
    /// adopts the live balances and refreshes growth ratios.
    ///
    /// `live_btc` and `live_usdt` are taken from `exchange.get_balances()`.
    /// Profit-split fields (`btc_treasury_vault`, `compound_balance`,
    /// trade counters) are preserved so the per-trade split ledger stays
    /// intact.
    pub fn resync_after_fill(&self, live_btc: f64, live_usdt: f64) {
        let mut state = self.get_treasury_state();
        // Anchor `previous_btc` to the pre-resync value so growth tracking
        // still has a valid base. If the field was 0 (first ever close),
        // use the live value as the anchor too.
        if state.previous_btc <= 0.0 {
            state.previous_btc = state.current_btc;
        }
        state.current_btc = live_btc;
        state.usdt_balance = live_usdt;
        state.stable_value = live_usdt;
        self.save_treasury_state(state);
        self.update_growth_ratios();
        tracing::info!(
            "Treasury re-synced after fill: BTC={:.8} USDT={:.2}",
            live_btc, live_usdt
        );
    }

    /// Deduct the QUOTE currency we just spent on a buy so the local ledger
    /// matches Binance. Without this, the next risk calc reads the pre-buy
    /// balance and sizes the next position as if the previous buy never
    /// happened — over-sizing compounds until live fills fail.
    ///
    /// `pair` is the trading pair (e.g. SOLBTC, BTCUSDT).
    /// `quote_spent` is the amount of quote currency spent (BTC for SOLBTC,
    /// USDT for BTCUSDT). Subtracted from the matching ledger field.
    pub fn deduct_balance_for_buy(&self, pair: &str, quote_spent: f64) {
        if quote_spent <= 0.0 {
            return;
        }
        let p = pair.to_uppercase();
        let mut state = self.get_treasury_state();
        if p.ends_with("BTC") && p != "BTCUSDT" {
            // BTC-quote pair: quote asset is BTC. We spent `quote_spent` BTC.
            state.current_btc = (state.current_btc - quote_spent).max(0.0);
            tracing::info!(
                "Treasury: deducted {:.8} BTC for {} buy → current_btc={:.8}",
                quote_spent, pair, state.current_btc
            );
        } else {
            // USDT (or USDC) quote pair: deduct from stable balance.
            state.usdt_balance = (state.usdt_balance - quote_spent).max(0.0);
            state.stable_value = state.usdt_balance;
            tracing::info!(
                "Treasury: deducted {:.2} USDT for {} buy → usdt_balance={:.2}",
                quote_spent, pair, state.usdt_balance
            );
        }
        self.save_treasury_state(state);
    }

    pub fn log_decision(&self, record: BtcDecisionRecord) {
        let _guard = self.lock.write().unwrap();
        let path = self.account_dir.join("btc-decision-log.json");
        let tmp_path = self.account_dir.join("btc-decision-log.json.tmp");
        let mut records: Vec<BtcDecisionRecord> = fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        records.push(record);
        let json = serde_json::to_string_pretty(&records).unwrap();
        if fs::write(&tmp_path, &json).is_err() {
            tracing::error!("memory: failed to write decision log tmp");
            return;
        }
        if fs::rename(&tmp_path, &path).is_err() {
            tracing::error!("memory: failed to rename decision log tmp");
            let _ = fs::remove_file(&tmp_path);
        }
    }

    pub fn get_decisions(&self) -> Vec<BtcDecisionRecord> {
        self.read_json("btc-decision-log.json", vec![])
    }

    pub fn get_config(&self) -> BtcConfig {
        self.read_json("btc-config.json", BtcConfig::default())
    }

    pub fn save_config(&self, config: &BtcConfig) {
        self.write_json("btc-config.json", config);
    }

    pub fn get_positions(&self) -> Vec<BtcAdvisoryPosition> {
        self.read_json("btc-positions.json", vec![])
    }

    #[allow(dead_code)]
    pub fn save_positions(&self, positions: &[BtcAdvisoryPosition]) {
        let _guard = self.lock.write().unwrap();
        let path = self.account_dir.join("btc-positions.json");
        let tmp_path = self.account_dir.join("btc-positions.json.tmp");
        let json = serde_json::to_string_pretty(positions).expect("Failed to serialize");
        if fs::write(&tmp_path, &json).is_err() {
            tracing::error!("memory: failed to write positions tmp");
            return;
        }
        if fs::rename(&tmp_path, &path).is_err() {
            tracing::error!("memory: failed to rename positions tmp");
            let _ = fs::remove_file(&tmp_path);
        }
    }

    pub fn get_lessons(&self) -> Vec<String> {
        self.read_json("btc-lessons.json", vec![])
    }

    pub fn add_lesson(&self, lesson: String) {
        let _guard = self.lock.write().unwrap();
        let path = self.account_dir.join("btc-lessons.json");
        let tmp_path = self.account_dir.join("btc-lessons.json.tmp");
        let mut lessons: Vec<String> = fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        lessons.push(lesson);
        let json = serde_json::to_string_pretty(&lessons).unwrap();
        if fs::write(&tmp_path, &json).is_err() {
            tracing::error!("memory: failed to write lessons tmp");
            return;
        }
        if fs::rename(&tmp_path, &path).is_err() {
            tracing::error!("memory: failed to rename lessons tmp");
            let _ = fs::remove_file(&tmp_path);
        }
    }

    /// Called when a position closes. Updates current_btc based on realized profit/loss.
    /// Accepts a live BTC price to convert USDT profit to BTC.
    /// Update treasury on position close.
    /// For BTC-quote pairs (SOLBTC): pass btc_price = 1.0 since PnL is already in BTC.
    /// For USDT-quote pairs (BTCUSDT): pass actual BTCUSDT price for USDT→BTC conversion.
    /// If `btc_price <= 0` for a USDT-quote pair the function returns false and
    /// does not write to state — callers must pass a real price.
    pub fn update_treasury_on_close(&self, pair: &str, pnl_pct: f64, position_size_quote: f64, btc_price: f64) -> bool {
        let cfg = self.get_config();
        let mut state = self.get_treasury_state();
        let pnl_multiplier = 1.0 + (pnl_pct / 100.0);
        let gross_pnl = position_size_quote * (pnl_multiplier - 1.0);

        // Fee deduction: entry + exit taker fee
        // Approximate exit value as entry value * (1 + pnl_pct/100)
        let exit_value = position_size_quote * pnl_multiplier;
        let round_trip_fee = (position_size_quote + exit_value) * cfg.taker_fee_pct;
        let net_pnl = gross_pnl - round_trip_fee;

        // btc_price == 1.0 signals a BTC-quote pair: PnL is already in BTC
        let is_btc_quote = (btc_price - 1.0).abs() < 1e-9;
        // For USDT-quote pairs we REQUIRE a real BTC price. If the caller
        // passed 0 (e.g. Binance price fetch failed), refuse to write — better
        // to skip the close than to silently corrupt treasury with a 65k fallback
        // that may be 50%+ off in either direction.
        if !is_btc_quote && btc_price <= 0.0 {
            tracing::error!(
                "Refusing to close {} — btc_price must be > 0 for USDT-quote pair (got {}). \
                 Fetch live BTCUSDT price before retrying.",
                pair, btc_price
            );
            return false;
        }
        let price = if is_btc_quote { 1.0 } else { btc_price };
        let btc_delta = if is_btc_quote { net_pnl } else { net_pnl / price };

        if pnl_pct > 0.0 {
            let vault_btc = btc_delta * cfg.treasury_pct;
            let compound_btc = btc_delta * cfg.compound_pct;
            // Capture pre-update btc as `previous_btc` so growth tracking
            // has a stable anchor. Without this, the `previous_btc` field
            // is permanently 0 and `btc_growth_7d` can never be computed.
            state.previous_btc = state.current_btc;
            state.current_btc += btc_delta;
            state.btc_treasury_vault += vault_btc;
            state.compound_balance += compound_btc;
            state.total_trades += 1;
            state.winning_trades += 1;
            let unit = if is_btc_quote { "BTC" } else { "USDT" };
            tracing::info!(
                "Position {} closed at +{:.2}%. BTC treasury grew by {:.8} BTC (profit: {:.2} {}, fee: {:.2} {}). Split: {:.8} vault + {:.8} compound",
                pair, pnl_pct, btc_delta, gross_pnl, unit, round_trip_fee, unit, vault_btc, compound_btc
            );
        } else {
            // Same growth-anchor logic on loss: previous_btc = btc before
            // the loss is applied, so growth calc measures the step.
            state.previous_btc = state.current_btc;
            state.current_btc = (state.current_btc + btc_delta).max(0.0);
            state.total_trades += 1;
            state.losing_trades += 1;
            let unit = if is_btc_quote { "BTC" } else { "USDT" };
            tracing::info!(
                "Position {} closed at {:.2}%. BTC treasury reduced by {:.8} BTC (loss: {:.2} {}, fee: {:.2} {})",
                pair, pnl_pct, btc_delta.abs(), gross_pnl.abs(), unit, round_trip_fee, unit
            );
        }

        self.save_treasury_state(state);
        // Refresh growth ratios so /btc_treasury shows real numbers.
        self.update_growth_ratios();
        true
    }

    pub fn load_skills(&self) -> String {
        // SKILL.md is shared across accounts and lives in the data_dir root.
        let path = self.data_dir.join("SKILL.md");
        fs::read_to_string(&path).unwrap_or_default()
    }

    pub fn load_lessons_context(&self) -> String {
        let lessons = self.get_lessons();
        if lessons.is_empty() {
            return String::new();
        }
        // Take only the 3 most recent lessons, each capped at 250 chars.
        // Previous behavior: 10 lessons at full length. With 379+ lessons
        // accumulating over time, this grew unbounded and added ~5-10KB to
        // every LLM call. The LLM doesn't need a history lesson — it needs
        // the freshest signal of what just went wrong.
        let recent: Vec<&String> = lessons.iter().rev().take(3).collect();
        let mut out = String::from("\n\nRECENT LESSONS (3 most recent):\n");
        for (i, l) in recent.iter().enumerate() {
            let truncated: String = if l.chars().count() > 250 {
                let mut s: String = l.chars().take(247).collect();
                s.push_str("...");
                s
            } else {
                (*l).clone()
            };
            out.push_str(&format!("{}. {}\n", i + 1, truncated));
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_account_uses_legacy_flat_layout() {
        let tmp = tempfile_or("./data/memory_test_legacy");
        let store = MemoryStore::new(&tmp);
        // Legacy: file should be at data_dir/btc-treasury.json
        let legacy = std::path::Path::new(&tmp).join("btc-treasury.json");
        assert!(legacy.exists(), "default account must use flat layout, expected {}", legacy.display());
        assert!(store.account_id().is_none());
        std::fs::remove_dir_all(&tmp).ok();
    }

    #[test]
    fn default_string_account_uses_legacy_layout() {
        // `Some("default")` is treated as the legacy single-account case so
        // an explicit "default" account (e.g. from the loader) does not
        // accidentally create a subdir and orphan the user's files.
        let tmp = "./data/memory_test_default_str";
        let store = MemoryStore::with_account(tmp, Some("default"), None);
        let legacy = std::path::Path::new(tmp).join("btc-treasury.json");
        assert!(legacy.exists());
        assert_eq!(store.account_id(), Some("default"));
        std::fs::remove_dir_all(tmp).ok();
    }

    #[test]
    fn named_account_uses_subdir() {
        let tmp = "./data/memory_test_named";
        let store = MemoryStore::with_account(tmp, Some("alpha"), None);
        let sub = std::path::Path::new(tmp).join("accounts").join("alpha").join("btc-treasury.json");
        assert!(sub.exists(), "named account must use subdir, expected {}", sub.display());
        // Legacy flat layout must NOT receive the named account's state.
        let legacy = std::path::Path::new(tmp).join("btc-treasury.json");
        assert!(!legacy.exists(), "named account must not write to legacy flat file");
        assert_eq!(store.account_id(), Some("alpha"));
        std::fs::remove_dir_all(tmp).ok();
    }

    fn tempfile_or(p: &str) -> String {
        p.to_string()
    }

    // ── Fase 3: layered (id, exchange) layout tests ──────────────────────────

    #[test]
    fn named_account_with_exchange_uses_exchange_subdir() {
        let tmp = "./data/memory_test_named_with_exchange";
        let store = MemoryStore::with_account(tmp, Some("main"), Some(ExchangeKind::Okx));
        let layered = std::path::Path::new(tmp)
            .join("accounts").join("main").join("okx").join("btc-treasury.json");
        assert!(layered.exists(), "named+exchange must use layered subdir, expected {}", layered.display());
        // Other layouts must NOT exist
        let flat = std::path::Path::new(tmp).join("btc-treasury.json");
        assert!(!flat.exists(), "named+exchange must not write to flat layout");
        let no_ex = std::path::Path::new(tmp).join("accounts").join("main").join("btc-treasury.json");
        assert!(!no_ex.exists(), "named+exchange must not write to no-exchange subdir");
        assert_eq!(store.exchange(), Some(ExchangeKind::Okx));
        std::fs::remove_dir_all(tmp).ok();
    }

    #[test]
    fn default_account_keeps_flat_layout_even_with_exchange() {
        // BACKWARD COMPAT: Fase 2 users with id=default under EXCHANGE_NAME=both
        // must keep their flat-layout state at data_dir/btc-treasury.json.
        // Adding an exchange to a "default" id does NOT escalate to a layered
        // subdir — that would orphan the user's existing files.
        let tmp = "./data/memory_test_default_with_exchange";
        let store = MemoryStore::with_account(tmp, Some("default"), Some(ExchangeKind::Okx));
        let flat = std::path::Path::new(tmp).join("btc-treasury.json");
        assert!(flat.exists(), "default+exchange must keep flat layout, expected {}", flat.display());
        let layered = std::path::Path::new(tmp)
            .join("accounts").join("default").join("okx").join("btc-treasury.json");
        assert!(!layered.exists(), "default+exchange must NOT escalate to layered subdir");
        assert_eq!(store.exchange(), Some(ExchangeKind::Okx));
        std::fs::remove_dir_all(tmp).ok();
    }

    #[test]
    fn two_exchanges_under_one_id_do_not_collide() {
        let tmp = "./data/memory_test_two_exchanges";
        let binance = MemoryStore::with_account(tmp, Some("main"), Some(ExchangeKind::Binance));
        let okx = MemoryStore::with_account(tmp, Some("main"), Some(ExchangeKind::Okx));

        // Write different treasury values to each.
        let mut s1 = binance.get_treasury_state();
        s1.current_btc = 0.01234567;
        binance.save_treasury_state(s1);

        let mut s2 = okx.get_treasury_state();
        s2.current_btc = 0.00543210;
        okx.save_treasury_state(s2);

        // Reload each store and confirm independence.
        let r1 = binance.get_treasury_state();
        let r2 = okx.get_treasury_state();
        assert!((r1.current_btc - 0.01234567).abs() < 1e-8,
            "Binance store BTC should be 0.01234567, got {}", r1.current_btc);
        assert!((r2.current_btc - 0.00543210).abs() < 1e-8,
            "OKX store BTC should be 0.00543210, got {}", r2.current_btc);

        std::fs::remove_dir_all(tmp).ok();
    }
}
