use std::fs;
use std::path::PathBuf;
use std::sync::RwLock;

use crate::models::*;

pub struct MemoryStore {
    data_dir: PathBuf,
    lock: RwLock<()>,
}

impl MemoryStore {
    pub fn new(data_dir: &str) -> Self {
        let dir = PathBuf::from(data_dir);
        fs::create_dir_all(&dir).expect("Failed to create data directory");
        let store = Self {
            data_dir: dir,
            lock: RwLock::new(()),
        };
        store.init_defaults();
        store
    }

    fn init_defaults(&self) {
        let defaults: Vec<(&str, &str)> = vec![
            ("btc-treasury.json", r#"{"current_btc":0,"previous_btc":0,"btc_growth_7d":0,"btc_growth_30d":0,"stable_value":0,"usdt_balance":0,"last_update":"","btc_treasury_vault":0,"compound_balance":0,"total_trades":0,"winning_trades":0,"losing_trades":0,"trading_paused_until":"","consecutive_losses":0}"#),
            ("btc-decision-log.json", "[]"),
            ("btc-config.json", r#"{"enabled":true,"llm_activation_threshold":0.75,"min_confidence":0.80,"max_exposure":0.50,"daily_loss_limit_btc":0.0005,"max_consecutive_losses":3,"safe_mode_volatility":9.0,"safe_mode_drawdown":0.05,"scanner_pairs":["BTCUSDT","SOLBTC","ETHBTC","BNBBTC","XRPBTC","ADABTC","LINKBTC","SUIBTC","AVAXBTC","DOGEBTC"],"take_profit_pct":5.5,"stop_loss_pct":-1.5,"trailing_tp_pct":3.0,"use_trailing":true,"max_positions":1,"risk_per_trade_pct":0.01,"initial_capital_usdt":50.0,"min_score_threshold":80.0,"compound_pct":0.50,"treasury_pct":0.50,"dry_run":true}"#),
            ("btc-positions.json", "[]"),
            ("btc-lessons.json", "[]"),
        ];

        // Write SKILL.md from source if exists, otherwise create default.
        // The Docker image bakes SKILL.md into the working directory; if not
        // found there, also check the project root (../SKILL.md) for dev mode.
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
            let path = self.data_dir.join(filename);
            if !path.exists() {
                fs::write(&path, content).expect("Failed to write default file");
            }
        }
    }

    fn read_json<T: serde::de::DeserializeOwned>(&self, filename: &str, default: T) -> T {
        let _guard = self.lock.read().unwrap();
        let path = self.data_dir.join(filename);
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
        let path = self.data_dir.join(filename);
        let tmp_path = self.data_dir.join(format!("{}.tmp", filename));
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
        let path = self.data_dir.join("btc-decision-log.json");
        let tmp_path = self.data_dir.join("btc-decision-log.json.tmp");
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
        let path = self.data_dir.join("btc-positions.json");
        let tmp_path = self.data_dir.join("btc-positions.json.tmp");
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
        let path = self.data_dir.join("btc-lessons.json");
        let tmp_path = self.data_dir.join("btc-lessons.json.tmp");
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
        let path = self.data_dir.join("SKILL.md");
        fs::read_to_string(&path).unwrap_or_default()
    }

    pub fn load_lessons_context(&self) -> String {
        let lessons = self.get_lessons();
        if lessons.is_empty() {
            return String::new();
        }
        let recent: Vec<&String> = lessons.iter().rev().take(10).collect();
        format!(
            "\n\nRECENT SELF-LEARNING LESSONS (learn from these):\n{}",
            recent
                .iter()
                .enumerate()
                .map(|(i, l)| format!("{}. {}", i + 1, l))
                .collect::<Vec<_>>()
                .join("\n")
        )
    }
}
