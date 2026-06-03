#![allow(dead_code)]
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use tokio::sync::RwLock;

use crate::llm::LlmClient;
use crate::memory::MemoryStore;
use crate::models::*;

const SYSTEM_PROMPT: &str = r#"BTC Treasury Accumulation AI. Goal: maximize Δ BTC (not USD).

CONSTRAINTS:
- SPOT only (no leverage/futures/perpetual). Universe: BTC-quote pairs.
- Max 1% risk/trade after fees. TP > |SL|. Max 1 active position.
- Score >= 80 → AMBIL. Score < 80 → DO NOTHING.
- 3 losses → 24h pause. Drawdown > 10% → reduce size 50%.
- Round-trip fee 0.2% (taker 0.1% × 2). SL must absorb fee.

DYNAMIC TP/SL BY REGIME (system auto-clamps SL to 1.5× ATR_14):
- CALM/RANGING: TP 2.5-4%, SL -0.8 to -1.2%
- TRENDING: TP 4-7%, SL -1.2 to -1.8%
- VOLATILE: TP 6-10%, SL -1.8 to -2.5%
- For positions ≤0.001 BTC: use upper TP, min SL (wider ratio).

ENTRY (all required): RS rising, EMA20>EMA50>EMA200, MACD bullish, vol>avg.
EXIT: TP, trailing stop (active), hard SL.

TREASURY: 50% compound + 50% BTC vault. Vault untouchable.

SCORING: 40% RS, 25% Volume, 20% Trend, 10% Vol quality, 5% Structure.

RECS: REJECT, MONITOR, APPROVE, REDUCE_EXPOSURE, EXIT_POSITION, PROTECT_TREASURY, ENABLE_SAFE_MODE.

PROHIBITED: predicting prices, guaranteeing profits, martingale, all-in, leverage, futures, USD-denominated success.

OUTPUT ONLY valid JSON. No markdown. No text outside JSON.

{
  "market_regime": "TRENDING_BULLISH",
  "opportunity_score": 82,
  "confidence": 0.84,
  "risk_level": "MEDIUM",
  "recommendation": "APPROVE",
  "treasury_mode": "ACCUMULATE",
  "reason": "Strong RS + Volume spike + EMA alignment.",
  "warnings": [],
  "dynamic_take_profit": 5.5,
  "dynamic_stop_loss": -1.2,
  "tp_reason": "TRENDING - 5.5% TP captures momentum above 4h resistance",
  "sl_reason": "1.2% SL + 0.2% fee = 1.4% max loss, below 1.5% support"
}"#;

const CACHE_TTL_SECS: u64 = 300; // 5 minutes
const PAIR_COOLDOWN_SECS: u64 = 300; // 5 minutes per pair
const MAX_LESSONS_IN_CONTEXT: usize = 3;
const MAX_LESSON_CHARS: usize = 250;

#[derive(Clone)]
struct CacheEntry {
    advisory: FullBtcAdvisory,
    cached_at: Instant,
}

pub struct AdvisoryEngine {
    llm: LlmClient,
    mem: Arc<MemoryStore>,
    /// (pair, regime_bucket, score_bucket) -> cached advisory
    cache: RwLock<HashMap<String, CacheEntry>>,
    /// pair -> last LLM call timestamp (for cooldown)
    last_llm_call: RwLock<HashMap<String, Instant>>,
}

impl AdvisoryEngine {
    pub fn new(llm_url: String, llm_model: String, llm_api_key: String, mem: Arc<MemoryStore>) -> Self {
        tracing::info!("BTC advisor: skills context dropped from LLM prompt to save tokens");
        Self {
            llm: LlmClient::new(llm_url, llm_model, llm_api_key),
            mem,
            cache: RwLock::new(HashMap::new()),
            last_llm_call: RwLock::new(HashMap::new()),
        }
    }

    pub async fn analyze(&self, input: &BtcAdvisoryInput) -> FullBtcAdvisory {
        let config = self.mem.get_config();

        // Single source of truth for regime — used by quant fast-path, cooldown
        // key, cache key, and quant_advisory. Computing it once here avoids
        // re-classifying inside helper functions.
        let market_regime = classify_regime(&input.market_data);
        let (risk_level, warnings) = assess_risk(&input.market_data, &input.treasury, input.loss_streak);
        let treasury_mode = treasury_mode(&input.market_data, &input.treasury, &risk_level);

        // Blend AI score with orderbook-based opportunity score.
        // - orderbook_score: 0-100 scale (from opportunity_score())
        // - ai_score: 0-10 scale from AIScoringEngine → multiply by 10 to
        //   normalize to 0-100 before blending.
        let orderbook_score = opportunity_score(&input.market_data); // 0-100
        let opportunity_score = match input.ai_score {
            Some(ai) if ai > 0.0 => {
                let ai_100 = (ai * 10.0).clamp(0.0, 100.0); // normalize to 0-100
                let ai_weight = 0.6;
                let ob_weight = 0.4;
                (ai_100 * ai_weight + orderbook_score * ob_weight).round().clamp(0.0, 100.0)
            }
            _ => orderbook_score,
        };

        // ── EARLY-EXIT CHEAP GUARDS ──────────────────────────────────────
        // These don't need LLM, scoring, or even a cache lookup. They handle
        // the loud majority of scanner ticks (clear rejects, loss streaks,
        // danger regimes) without paying any per-call cost beyond a
        // regime + risk classification. Placing them BEFORE the cooldown
        // also makes them independent of any LLM history.
        if let Some(quant_decision) = quant_fast_path(
            &input.market_data,
            &input.treasury,
            opportunity_score,
            &risk_level,
            &market_regime,
            input.loss_streak,
        ) {
            tracing::debug!(
                "BTC [{}]: quant fast-path → {} (score={:.0}, risk={})",
                input.market_data.pair, quant_decision.recommendation, opportunity_score, risk_level
            );
            return quant_decision;
        }

        // ── LLM CACHE LOOKUP (before cooldown to maximize hit rate) ─────
        // Cache is regime+pair+score-bucketed. Same regime + same bucket
        // = same advisory, even if we technically cleared the cooldown.
        // Checking the cache BEFORE the cooldown doubles our effective hit
        // rate for back-to-back scans of the same pair in the same regime.
        let cache_key = self.cache_key(&input.market_data.pair, &market_regime, opportunity_score);
        if let Some(cached) = self.cache_get(&cache_key).await {
            tracing::debug!("BTC [{}]: LLM cache hit (key={})", input.market_data.pair, cache_key);
            return cached;
        }

        // ── SHOULD WE ACTUALLY CALL LLM? ─────────────────────────────────
        // After the fast-path, the remaining cases are "ambiguous". But
        // even then, if the quant signal is already strong enough (LOW risk
        // + reasonable score) we let quant decide. LLM is reserved for
        // genuinely uncertain cases: MEDIUM risk, or LOW risk with mediocre
        // score, or distress signals.
        if !should_activate_llm(opportunity_score, &input.market_data, &risk_level, &market_regime, &config) {
            return quant_advisory(
                &input.market_data,
                &market_regime,
                &risk_level,
                &warnings,
                opportunity_score,
                &treasury_mode,
                config.taker_fee_pct,
            );
        }

        // ── COOLDOWN GATE ────────────────────────────────────────────────
        // Don't call LLM twice in a row for the same pair within 5 min.
        // This is the per-pair LLM rate limit. The cache check above means
        // we usually skip the LLM call entirely even before reaching here.
        if !self.cooldown_elapsed(&input.market_data.pair, &market_regime).await {
            tracing::debug!(
                "BTC [{}]: cooldown active, returning quant fallback",
                input.market_data.pair
            );
            return quant_advisory(
                &input.market_data,
                &market_regime,
                &risk_level,
                &warnings,
                opportunity_score,
                &treasury_mode,
                config.taker_fee_pct,
            );
        }

        if !config.enabled {
            return quant_advisory(
                &input.market_data,
                &market_regime,
                &risk_level,
                &warnings,
                opportunity_score,
                &treasury_mode,
                config.taker_fee_pct,
            );
        }

        // ── ACTUAL LLM CALL ──────────────────────────────────────────────
        tracing::info!("BTC Advisory [{}]: activating LLM", input.market_data.pair);
        match self.call_llm(input, &market_regime, &risk_level, &warnings, opportunity_score, &treasury_mode).await {
            Ok(mut advisory) => {
                advisory.opportunity_score = opportunity_score;
                advisory.market_regime = market_regime.clone();
                advisory.bypass_quant = true;
                self.cache_put(cache_key, advisory.clone()).await;
                self.mark_llm_called(&input.market_data.pair, &market_regime).await;
                advisory
            }
            Err(e) => {
                tracing::error!("BTC LLM call failed: {}", e);
                quant_advisory(&input.market_data, &market_regime, &risk_level, &warnings, opportunity_score, &treasury_mode, config.taker_fee_pct)
            }
        }
    }

    async fn call_llm(
        &self,
        input: &BtcAdvisoryInput,
        regime: &str,
        risk_level: &str,
        warnings: &[String],
        opportunity_score: f64,
        treasury_mode: &str,
    ) -> anyhow::Result<FullBtcAdvisory> {
        let config = self.mem.get_config();
        let warnings_str = if warnings.is_empty() { "none".to_string() } else { warnings.join("; ") };

        // Compact position representation: just pair+pnl+side, not the full struct.
        let positions_str = if input.open_positions.is_empty() {
            "none".to_string()
        } else {
            input.open_positions.iter()
                .map(|p| format!("{}({}%)", p.id, p.pnl_btc))
                .collect::<Vec<_>>()
                .join(", ")
        };

        // Ringkas: drop verbose pair_metrics JSON, replace with one-line indicator summary.
        let metrics_summary = input.pair_metrics.as_ref().map(|pm| {
            format!(
                "RS={:.2} EMA={} MACD={} VolSpike={} ATR%={:.2} Spread={:.2} BidDepth={:.0} AskDepth={:.0}",
                pm.rs_score,
                if pm.ema_bullish_alignment { "bull" } else { "bear" },
                if pm.macd_bullish { "bull" } else { "bear" },
                pm.volume_spike,
                pm.atr_14,
                pm.spread_pct,
                pm.bid_depth,
                pm.ask_depth,
            )
        }).unwrap_or_else(|| "n/a".to_string());

        let ai_score_str = input.ai_score.map(|s| format!("{:.0}", s)).unwrap_or_else(|| "n/a".into());

        let user_prompt = format!(
            "Pair={} Regime={} Risk={} Mode={} Score={:.0} Conf={:.2} \
Trend={:.1} Vol={:.1} Liq={:.1} Spread={:.1} Volatility={:.1} \
BreakoutProb={:.2} ReversalProb={:.2} Exposure={:.2} DD={:.4} \
FeeRT={:.2}% LossStreak={} AI={} \
Indicators: {} Positions: {} Warnings: {} \
BTC={:.8} PrevBTC={:.8} Growth7d={:.4} \
Strategy={}",
            input.market_data.pair,
            regime,
            risk_level,
            treasury_mode,
            opportunity_score,
            input.market_data.confidence,
            input.market_data.trend_strength,
            input.market_data.volume_score,
            input.market_data.liquidity_score,
            input.market_data.spread_score,
            input.market_data.volatility_score,
            input.market_data.breakout_probability,
            input.market_data.reversal_probability,
            input.market_data.portfolio_exposure,
            input.market_data.daily_drawdown,
            config.taker_fee_pct * 200.0,
            input.loss_streak,
            ai_score_str,
            metrics_summary,
            positions_str,
            warnings_str,
            input.treasury.current_btc,
            input.treasury.previous_btc,
            input.treasury.btc_growth_7d,
            input.market_data.active_strategy,
        );

        let lessons_ctx = self.mem.load_lessons_context();

        tracing::debug!(
            "BTC LLM prompt sizes: system={} user={} lessons={} (B)",
            SYSTEM_PROMPT.len(), user_prompt.len(), lessons_ctx.len()
        );
        self.llm.call(SYSTEM_PROMPT, &format!("{}{}", user_prompt, lessons_ctx)).await
    }

    // ── Cache helpers ────────────────────────────────────────────────────

    fn cache_key(&self, pair: &str, regime: &str, score: f64) -> String {
        // Bucket score into 5-point buckets to maximize cache hits on
        // nearly-identical scans.
        let score_bucket = (score / 5.0).floor() as i32;
        format!("{}|{}|{}", pair, regime, score_bucket)
    }

    async fn cache_get(&self, key: &String) -> Option<FullBtcAdvisory> {
        let cache = self.cache.read().await;
        if let Some(entry) = cache.get(key) {
            if entry.cached_at.elapsed() < Duration::from_secs(CACHE_TTL_SECS) {
                return Some(entry.advisory.clone());
            }
        }
        None
    }

    async fn cache_put(&self, key: String, advisory: FullBtcAdvisory) {
        let mut cache = self.cache.write().await;
        cache.insert(key, CacheEntry { advisory, cached_at: Instant::now() });
        // Opportunistic GC: cap cache at 256 entries
        if cache.len() > 256 {
            let cutoff = Instant::now() - Duration::from_secs(CACHE_TTL_SECS);
            cache.retain(|_, v| v.cached_at > cutoff);
        }
    }

    // ── Cooldown helpers ─────────────────────────────────────────────────

    async fn cooldown_elapsed(&self, pair: &str, _regime: &str) -> bool {
        let map = self.last_llm_call.read().await;
        if let Some(last) = map.get(pair) {
            // If regime changed, allow LLM call (regime change is a real signal)
            // but we don't track previous regime per pair; just enforce time.
            return last.elapsed() >= Duration::from_secs(PAIR_COOLDOWN_SECS);
        }
        true
    }

    async fn mark_llm_called(&self, pair: &str, _regime: &str) {
        let mut map = self.last_llm_call.write().await;
        map.insert(pair.to_string(), Instant::now());
        if map.len() > 64 {
            // GC: drop entries older than 2x cooldown
            let cutoff = Instant::now() - Duration::from_secs(PAIR_COOLDOWN_SECS * 2);
            map.retain(|_, v| *v > cutoff);
        }
    }
}

// ── Quant Functions ──────────────────────────────────────────────────────

/// Fast-path decisions that don't need LLM reasoning. Returns Some(advisory)
/// when the quant signal is already clear enough to act on. Returns None to
/// fall through to the LLM path.
///
/// `market_regime` is passed in (not re-computed) so the caller pays the
/// classification cost exactly once. The caller also already knows
/// `risk_level` and `opportunity` — we trust those inputs to be canonical.
fn quant_fast_path(
    data: &BtcMarketData,
    treasury: &BtcTreasuryState,
    opportunity: f64,
    risk_level: &str,
    market_regime: &str,
    loss_streak: i32,
) -> Option<FullBtcAdvisory> {
    let taker_fee = 0.001_f64;

    // ── Danger regimes first: never ask LLM when the market is in a known
    //    unsafe state. These regimes are loud signals; LLM would just
    //    re-derive the same conclusion. ───────────────────────────────
    if market_regime == "LOW_LIQUIDITY_DANGER" || market_regime == "HIGH_VOLATILITY_DANGER" || market_regime == "PANIC_SELLOFF" {
        let mode = "SAFE_MODE".to_string();
        return Some(quant_advisory(data, market_regime, "CRITICAL", &Vec::new(), opportunity, &mode, taker_fee));
    }

    // ── Loss-streak circuit breaker: don't even ask LLM, just protect. ─
    if loss_streak >= 3 {
        let mode = treasury_mode(data, treasury, "HIGH");
        return Some(quant_advisory(
            data,
            market_regime,
            "HIGH",
            &vec!["Loss streak >= 3".into()],
            opportunity,
            &mode,
            taker_fee,
        ));
    }

    // ── Clear rejection zone: low score + any non-LOW risk → reject ────
    if opportunity < 50.0 && risk_level != "LOW" {
        let mode = treasury_mode(data, treasury, risk_level);
        return Some(quant_advisory(data, market_regime, risk_level, &Vec::new(), opportunity, &mode, taker_fee));
    }

    // ── FAKE_BREAKOUT / CHOPPY / DISTRIBUTION regimes are never
    //    approved — quant knows better. Skip LLM. ────────────────────
    if market_regime == "FAKE_BREAKOUT" || market_regime == "CHOPPY" || market_regime == "DISTRIBUTION" {
        let mode = treasury_mode(data, treasury, risk_level);
        let eff_risk = if risk_level == "LOW" { "MEDIUM" } else { risk_level };
        return Some(quant_advisory(data, market_regime, eff_risk, &Vec::new(), opportunity, &mode, taker_fee));
    }

    // ── TRENDING_BEARISH: never approve, but cap at MEDIUM (not HIGH) so
    //    the quant path returns REDUCE_EXPOSURE / MONITOR rather than
    //    the noisy PROTECT_TREASURY. ─────────────────────────────────
    if market_regime == "TRENDING_BEARISH" {
        let mode = "REDUCE_RISK".to_string();
        return Some(quant_advisory(data, market_regime, "HIGH", &Vec::new(), opportunity, &mode, taker_fee));
    }

    // ── Strong quant signal with low risk and high score: approve without
    //    LLM. Use regime-based TP/SL from quant_advisory so the fast-path
    //    trade quality matches LLM-path quality. ─────────────────────
    if risk_level == "LOW" && opportunity >= 80.0 && data.confidence >= 0.85 {
        let mode = treasury_mode(data, treasury, risk_level);
        return Some(quant_advisory(data, market_regime, risk_level, &Vec::new(), opportunity, &mode, taker_fee));
    }

    // ── MEDIUM risk + clear non-approval: reject without LLM. ─────────
    if risk_level == "MEDIUM" && opportunity < 70.0 {
        let mode = treasury_mode(data, treasury, risk_level);
        return Some(quant_advisory(data, market_regime, risk_level, &Vec::new(), opportunity, &mode, taker_fee));
    }

    None
}


fn classify_regime(data: &BtcMarketData) -> String {
    // Guard: extreme danger conditions first
    if data.liquidity_score < 3.0 && data.volume_score < 3.0 {
        return "LOW_LIQUIDITY_DANGER".into();
    }
    if data.volatility_score > 9.0 {
        return "HIGH_VOLATILITY_DANGER".into();
    }
    // Panic: strong negative trend + high vol
    if data.trend_strength < -8.0 && data.volatility_score > 7.0 {
        return "PANIC_SELLOFF".into();
    }
    // Trending states (strongest signals first)
    if data.trend_strength > 7.0 && data.volume_score > 6.0 && data.breakout_probability > 0.6 {
        return "TRENDING_BULLISH".into();
    }
    if data.trend_strength < -7.0 && data.volume_score > 6.0 {
        return "TRENDING_BEARISH".into();
    }
    // Fake breakout: reversal likely at high trend
    if data.reversal_probability > 0.75 && data.trend_strength > 5.0 {
        return "FAKE_BREAKOUT".into();
    }
    // Breakout expansion
    if data.breakout_probability > 0.75 && data.trend_strength > 0.0 {
        return "BREAKOUT_EXPANSION".into();
    }
    // Distribution: bearish trend + volume
    if data.trend_strength < -3.0 && data.volume_score > 5.0 {
        return "DISTRIBUTION".into();
    }
    // Accumulation: bullish trend, lower volume (stealth accumulation)
    if data.trend_strength > 3.0 && data.volume_score < 5.0 && data.breakout_probability < 0.35 {
        return "ACCUMULATION".into();
    }
    // Choppy: low confidence + low volume
    if (data.confidence < 0.4 && data.volume_score < 4.0)
        || (data.trend_strength.abs() < 2.0 && data.confidence < 0.35)
    {
        return "CHOPPY".into();
    }
    // Ranging: sideways price action
    if data.trend_strength.abs() < 3.0 && data.volume_score > 3.0 {
        return "RANGING".into();
    }
    "RANGING".into()
}


fn assess_risk(data: &BtcMarketData, treasury: &BtcTreasuryState, loss_streak: i32) -> (String, Vec<String>) {
    let mut risk_score: f64 = 0.0;
    let mut warnings: Vec<String> = Vec::new();

    if data.liquidity_score < 4.0 {
        risk_score += 3.0;
        warnings.push("Liquidity critically low".into());
    }
    if data.spread_score < 4.0 {
        risk_score += 3.0;
        warnings.push("Spread critically wide".into());
    }
    if data.volatility_score > 9.0 {
        risk_score += 3.0;
        warnings.push("Extreme volatility".into());
    }
    if data.daily_drawdown > 0.03 {
        risk_score += 2.0;
        warnings.push("Daily drawdown exceeding 3%".into());
    }
    if data.daily_drawdown > 0.05 {
        risk_score += 2.0;
        warnings.push("Daily drawdown exceeding 5%".into());
    }
    if loss_streak >= 3 {
        risk_score += 2.0;
        warnings.push(format!("Loss streak: {} consecutive losses", loss_streak));
    }
    if data.confidence < 0.5 {
        risk_score += 1.0;
        warnings.push("Low confidence signal".into());
    }
    if data.reversal_probability > 0.6 {
        risk_score += 2.0;
        warnings.push("High reversal probability".into());
    }
    if treasury.btc_growth_7d < -0.05 {
        risk_score += 1.0;
        warnings.push("7-day BTC treasury decline".into());
    }
    if data.portfolio_exposure > 0.40 {
        risk_score += 1.0;
        warnings.push("Portfolio exposure above 40%".into());
    }

    let level = if risk_score >= 7.0 {
        "CRITICAL"
    } else if risk_score >= 4.0 {
        "HIGH"
    } else if risk_score >= 2.0 {
        "MEDIUM"
    } else {
        if warnings.is_empty() {
            warnings.push("No significant risk factors".into());
        }
        "LOW"
    };

    (level.to_string(), warnings)
}

fn treasury_mode(data: &BtcMarketData, treasury: &BtcTreasuryState, risk_level: &str) -> String {
    if risk_level == "CRITICAL" {
        return "SAFE_MODE".into();
    }
    if data.liquidity_score < 4.0 || data.spread_score < 4.0 || data.volatility_score > 9.0 {
        return "SAFE_MODE".into();
    }
    if risk_level == "HIGH" {
        return "REDUCE_RISK".into();
    }
    if data.daily_drawdown > 0.04 || treasury.btc_growth_7d < -0.03 {
        return "PROTECT".into();
    }
    if data.trend_strength > 4.0 && data.confidence > 0.65 && risk_level == "LOW" {
        return "ACCUMULATE".into();
    }
    if data.trend_strength > 2.0 && risk_level == "MEDIUM" {
        return "ACCUMULATE".into();
    }
    "PROTECT".into()
}

fn opportunity_score(data: &BtcMarketData) -> f64 {
    let trend_norm = ((data.trend_strength + 10.0) / 20.0).clamp(0.0, 1.0);

    // Each component is on a 0-10 scale; weights sum to 1.0 → total range 0-10.
    // Multiply by 10 to produce a 0-100 scale consistent with:
    //   - quant_fast_path threshold: opportunity >= 80.0
    //   - quant_advisory thresholds: >= 75.0, >= 60.0, < 50.0
    //   - config.min_score_threshold: default 80.0
    //   - should_activate_llm ambiguous zone: [55, 80)
    let score = data.liquidity_score * 0.20
        + data.spread_score * 0.10
        + (10.0 - data.volatility_score) * 0.15
        + data.volume_score * 0.15
        + trend_norm * 10.0 * 0.20
        + data.breakout_probability * 10.0 * 0.15
        + (1.0 - data.reversal_probability) * 10.0 * 0.05;

    // Scale to 0-100 and round to 1 decimal place.
    (score * 10.0 * 10.0).round() / 10.0
}

/// Activation gate. Runs AFTER the quant fast-path, so it only sees the
/// truly ambiguous cases. LLM is reserved for:
/// - Ambiguous opportunity zone ([60, 80)): quant could go either way
/// - Conflicting signals (confidence low but score decent)
/// - Distress conditions (drawdown > 3%, liquidity shock, or LOW risk
///   but with significant warnings the LLM might catch)
/// Returns true → call LLM. Returns false → use quant fallback.
///
/// Inputs are pre-computed (opportunity, risk_level, regime) so this
/// function does not pay any classification cost — it stays O(1).
fn should_activate_llm(
    opportunity: f64,
    data: &BtcMarketData,
    risk_level: &str,
    market_regime: &str,
    cfg: &BtcConfig,
) -> bool {
    // LLM for ambiguous opportunity zone.
    // Below 60: quant can decide (rejects, monitors).
    // 80+: fast-path already approved if LOW risk; otherwise LLM noise.
    if opportunity >= 60.0 && opportunity < 80.0 {
        return true;
    }
    // LLM for low-confidence signals (even if score is ok).
    if data.confidence < cfg.llm_activation_threshold {
        return true;
    }
    // LLM for distress conditions (these are unusual — LLM context helps).
    if data.daily_drawdown > 0.03 {
        return true;
    }
    // LLM only when the regime is genuinely uncertain: TRENDING regimes
    // and BREAKOUT_EXPANSION/ACCUMULATION are non-noisy. CHOPPY/FAKE/etc.
    // already short-circuited in the fast-path.
    if (data.volatility_score > cfg.safe_mode_volatility || data.liquidity_score < 4.0)
        && (market_regime == "TRENDING_BULLISH"
            || market_regime == "TRENDING_BEARISH"
            || market_regime == "BREAKOUT_EXPANSION"
            || market_regime == "ACCUMULATION")
    {
        return true;
    }
    // LLM for MEDIUM risk with decent score — the borderline case where
    // quant defaults to MONITOR/REDUCE but LLM could approve on nuance.
    if risk_level == "MEDIUM" && opportunity >= 70.0 && opportunity < 80.0 {
        return true;
    }
    false
}


fn quant_advisory(
    data: &BtcMarketData,
    regime: &str,
    risk_level: &str,
    warnings: &[String],
    opportunity: f64,
    treasury_mode: &str,
    taker_fee_pct: f64,
) -> FullBtcAdvisory {
    let round_trip_fee_pct = taker_fee_pct * 200.0; // e.g. 0.001 × 200 = 0.2%

    // ── Regime-aware dynamic TP/SL ─────────────────────────────────────────
    // Match what the system prompt tells the LLM to do (CALM/TRENDING/VOLATILE
    // bands) so the quant-path and LLM-path produce comparable trade quality.
    //
    // Fee awareness: effective loss = |SL%| + round_trip_fee.
    // We ensure |SL%| >= 0.8% after fee so the position survives random noise.
    let (tp, sl, tp_reason, sl_reason) = match regime {
        // Danger: these should never reach quant_advisory with APPROVE, but guard anyway
        "HIGH_VOLATILITY_DANGER" | "PANIC_SELLOFF" | "LOW_LIQUIDITY_DANGER" => {
            let sl = -(2.5_f64.max(round_trip_fee_pct + 2.0));
            (7.5, sl,
             "Danger regime — wide TP needed if position must be held".into(),
             format!("Danger regime — {:.1}% SL + {:.1}% fee", sl.abs(), round_trip_fee_pct))
        }
        // Trending: ride the momentum, wider targets
        "TRENDING_BULLISH" | "BREAKOUT_EXPANSION" => {
            let base_sl = 1.5_f64.max(round_trip_fee_pct + 0.8);
            let sl = -base_sl.min(2.0);
            let tp = if data.confidence >= 0.85 { 7.0 } else { 5.5 };
            (tp, sl,
             format!("TRENDING regime — {:.1}% TP captures momentum above resistance", tp),
             format!("TRENDING SL {:.1}% + {:.1}% fee = {:.1}% max loss",
                     base_sl, round_trip_fee_pct, base_sl + round_trip_fee_pct))
        }
        // Ranging/Accumulation: smaller move, tighter targets
        "RANGING" | "ACCUMULATION" => {
            let base_sl = 1.0_f64.max(round_trip_fee_pct + 0.8);
            let sl = -(base_sl.min(1.5));
            let tp = if opportunity >= 75.0 { 4.0 } else { 3.0 };
            (tp, sl,
             format!("RANGING/ACCUMULATION — {:.1}% TP for sideways breakout", tp),
             format!("CALM SL {:.1}% + {:.1}% fee = {:.1}% max loss",
                     base_sl, round_trip_fee_pct, base_sl + round_trip_fee_pct))
        }
        // High volatility zone: wide targets, wider SL room
        _ if data.volatility_score >= 7.0 => {
            let base_sl = 2.0_f64.max(round_trip_fee_pct + 1.5);
            let sl = -(base_sl.min(2.5));
            let tp = if data.confidence >= 0.80 { 8.0 } else { 6.0 };
            (tp, sl,
             format!("VOLATILE — {:.1}% TP for high-ATR environment", tp),
             format!("VOLATILE SL {:.1}% + {:.1}% fee = {:.1}% max loss",
                     base_sl, round_trip_fee_pct, base_sl + round_trip_fee_pct))
        }
        // Default (DISTRIBUTION, FAKE_BREAKOUT, CHOPPY, etc.) — conservative
        _ => {
            let base_sl = 0.8_f64.max(round_trip_fee_pct + 0.6);
            let sl = -(base_sl.clamp(0.8, 2.0));
            (5.5, sl,
             format!("Quant fallback: 5.5% TP (regime: {})", regime),
             format!("Quant fallback: {:.1}% SL + {:.1}% fee = {:.1}% max loss",
                     base_sl, round_trip_fee_pct, base_sl + round_trip_fee_pct))
        }
    };

    let (recommendation, reason) = match risk_level {
        "CRITICAL" => (
            "ENABLE_SAFE_MODE",
            "CRITICAL risk level — treasury protection activated.",
        ),
        "HIGH" => (
            "PROTECT_TREASURY",
            "HIGH risk detected — prioritize treasury protection.",
        ),
        "MEDIUM" if opportunity > 60.0 => (
            "MONITOR",
            "Medium risk with acceptable opportunity score. Monitor for improvement.",
        ),
        "MEDIUM" => (
            "REDUCE_EXPOSURE",
            "Medium risk with low opportunity. Reduce exposure.",
        ),
        _ if opportunity >= 75.0 && data.confidence >= 0.80 => (
            "APPROVE",
            "High opportunity score with strong confidence.",
        ),
        _ if opportunity >= 60.0 => (
            "MONITOR",
            "Opportunity meets baseline. Monitor for confirmation.",
        ),
        "LOW" if opportunity < 50.0 => (
            "PROTECT_TREASURY",
            "Low risk but unactionable opportunity. Preserve treasury.",
        ),
        _ => (
            "REJECT",
            "Weak opportunity — no trade is better than a low-confidence trade.",
        ),
    };

    FullBtcAdvisory {
        recommendation: recommendation.to_string(),
        confidence: data.confidence,
        risk_level: risk_level.to_string(),
        treasury_mode: treasury_mode.to_string(),
        reason: reason.to_string(),
        warnings: warnings.to_vec(),
        market_regime: regime.to_string(),
        opportunity_score: opportunity,
        bypass_quant: false,
        timestamp: chrono::Utc::now().to_rfc3339(),
        dynamic_take_profit: tp,
        dynamic_stop_loss: sl,
        tp_reason,
        sl_reason,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mk_data(
        confidence: f64,
        trend_strength: f64,
        volume_score: f64,
        liquidity_score: f64,
        spread_score: f64,
        volatility_score: f64,
        breakout_probability: f64,
        reversal_probability: f64,
        daily_drawdown: f64,
    ) -> BtcMarketData {
        BtcMarketData {
            pair: "SOLBTC".into(),
            market_regime: "RANGING".into(),
            trend_strength,
            volume_score,
            liquidity_score,
            spread_score,
            volatility_score,
            breakout_probability,
            reversal_probability,
            confidence,
            active_strategy: "default".into(),
            portfolio_exposure: 0.0,
            daily_drawdown,
        }
    }

    // ── should_activate_llm tests ─────────────────────────────────────

    #[test]
    fn test_should_activate_llm_skips_clean_low_risk_signal() {
        // Need opp ≥ 80 with low risk + high confidence — the perfect-bull case.
        let data = mk_data(0.90, 9.0, 8.0, 8.0, 8.0, 3.0, 0.7, 0.2, 0.0);
        let cfg = BtcConfig::default();
        let opp = opportunity_score(&data);
        assert!(opp >= 80.0, "Test precondition: opp should be ≥ 80, got {}", opp);
        // LOW risk: not in [60, 80), confidence is high, no distress.
        assert!(!should_activate_llm(opp, &data, "LOW", "RANGING", &cfg));
    }

    #[test]
    fn test_should_activate_llm_activates_for_ambiguous_zone() {
        // Need opp in [60, 80). Construct inputs that hit that band:
        //   trend=4, vol=6, liq=7, spr=7, vol_score=4, brk=0.5, rev=0.3
        let data = mk_data(0.90, 4.0, 6.0, 7.0, 7.0, 4.0, 0.5, 0.3, 0.0);
        let cfg = BtcConfig::default();
        let opp = opportunity_score(&data);
        assert!(opp >= 60.0 && opp < 80.0,
                "Test precondition: opp should be in [60, 80), got {}", opp);
        assert!(should_activate_llm(opp, &data, "LOW", "RANGING", &cfg));
    }

    #[test]
    fn test_should_activate_llm_activates_for_low_confidence() {
        // Need opp ≥ 80 (so it's not blocked by the ambiguous-zone gate),
        // and confidence below threshold so the low-conf gate fires.
        let data = mk_data(0.50, 9.0, 8.0, 8.0, 8.0, 3.0, 0.7, 0.2, 0.0);
        let cfg = BtcConfig { llm_activation_threshold: 0.85, ..BtcConfig::default() };
        let opp = opportunity_score(&data);
        assert!(opp >= 80.0, "Test precondition: opp should be ≥ 80, got {}", opp);
        assert!(should_activate_llm(opp, &data, "LOW", "RANGING", &cfg));
    }

    #[test]
    fn test_should_activate_llm_activates_for_drawdown_distress() {
        // 5% drawdown, otherwise clean → must call LLM.
        let data = mk_data(0.90, 5.0, 7.0, 7.0, 8.0, 4.0, 0.5, 0.2, 0.05);
        let cfg = BtcConfig::default();
        let opp = opportunity_score(&data);
        assert!(should_activate_llm(opp, &data, "LOW", "TRENDING_BULLISH", &cfg));
    }

    #[test]
    fn test_should_activate_llm_skips_when_score_below_60() {
        // Score < 60, clean signal → no LLM (quant rejects).
        let data = mk_data(0.90, -5.0, 3.0, 5.0, 6.0, 5.0, 0.1, 0.5, 0.0);
        let cfg = BtcConfig::default();
        let opp = opportunity_score(&data);
        assert!(opp < 60.0, "Test precondition: opp should be < 60, got {}", opp);
        assert!(!should_activate_llm(opp, &data, "LOW", "RANGING", &cfg));
    }

    // ── quant_fast_path tests ─────────────────────────────────────────

    #[test]
    fn test_quant_fast_path_handles_danger_regimes() {
        // HIGH_VOLATILITY_DANGER regime → quant handles, no LLM.
        let data = mk_data(0.5, 0.0, 3.0, 3.0, 3.0, 10.0, 0.5, 0.5, 0.0);
        let treasury = BtcTreasuryState::default();
        let result = quant_fast_path(&data, &treasury, 30.0, "LOW", "HIGH_VOLATILITY_DANGER", 0);
        assert!(result.is_some(), "Danger regime should fast-path");
        let adv = result.unwrap();
        assert_eq!(adv.recommendation, "ENABLE_SAFE_MODE");
    }

    #[test]
    fn test_quant_fast_path_handles_loss_streak() {
        // 3 consecutive losses → quant handles, no LLM.
        let data = mk_data(0.7, 3.0, 6.0, 6.0, 7.0, 5.0, 0.4, 0.3, 0.0);
        let treasury = BtcTreasuryState::default();
        let result = quant_fast_path(&data, &treasury, 65.0, "LOW", "TRENDING_BULLISH", 3);
        assert!(result.is_some(), "Loss streak >= 3 should fast-path");
        let adv = result.unwrap();
        assert!(adv.recommendation == "PROTECT_TREASURY" || adv.recommendation == "ENABLE_SAFE_MODE",
                "Loss streak should be protective, got {}", adv.recommendation);
    }

    #[test]
    fn test_quant_fast_path_handles_fake_breakout() {
        // FAKE_BREAKOUT regime → quant rejects, no LLM.
        let data = mk_data(0.6, 6.0, 5.0, 5.0, 5.0, 5.0, 0.3, 0.8, 0.0);
        let treasury = BtcTreasuryState::default();
        let result = quant_fast_path(&data, &treasury, 55.0, "LOW", "FAKE_BREAKOUT", 0);
        assert!(result.is_some(), "FAKE_BREAKOUT should fast-path");
        let adv = result.unwrap();
        assert!(adv.recommendation != "APPROVE",
                "FAKE_BREAKOUT should never be approved, got {}", adv.recommendation);
    }

    #[test]
    fn test_quant_fast_path_handles_trending_bearish() {
        // TRENDING_BEARISH → quant handles with REDUCE_EXPOSURE.
        let data = mk_data(0.6, -8.0, 7.0, 6.0, 6.0, 5.0, 0.2, 0.5, 0.0);
        let treasury = BtcTreasuryState::default();
        let result = quant_fast_path(&data, &treasury, 50.0, "MEDIUM", "TRENDING_BEARISH", 0);
        assert!(result.is_some(), "TRENDING_BEARISH should fast-path");
        let adv = result.unwrap();
        assert_ne!(adv.recommendation, "APPROVE",
                   "TRENDING_BEARISH should never be approved");
    }

    #[test]
    fn test_quant_fast_path_handles_strong_approval() {
        // LOW risk, opp >= 80, confidence >= 0.85 → APPROVE without LLM.
        // We construct inputs that hit opp ≥ 80 and run the fast-path directly.
        let data = mk_data(0.90, 9.0, 8.0, 8.0, 8.0, 3.0, 0.7, 0.2, 0.0);
        let treasury = BtcTreasuryState::default();
        let opp = opportunity_score(&data);
        assert!(opp >= 80.0, "Test precondition: opp should be ≥ 80, got {}", opp);
        let result = quant_fast_path(&data, &treasury, opp, "LOW", "TRENDING_BULLISH", 0);
        assert!(result.is_some(), "Strong LOW-risk signal should fast-path APPROVE");
        let adv = result.unwrap();
        assert_eq!(adv.recommendation, "APPROVE");
        // dynamic TP/SL must be set so the position has exits.
        assert!(adv.dynamic_take_profit > 0.0, "TP must be set");
        assert!(adv.dynamic_stop_loss < 0.0, "SL must be set");
    }

    #[test]
    fn test_quant_fast_path_falls_through_for_truly_ambiguous() {
        // MEDIUM risk, score in [60, 80) → fall through to LLM gate.
        let data = mk_data(0.85, 2.0, 5.0, 6.0, 6.0, 5.0, 0.3, 0.3, 0.0);
        let treasury = BtcTreasuryState::default();
        let opp = opportunity_score(&data);
        let result = quant_fast_path(&data, &treasury, opp, "MEDIUM", "RANGING", 0);
        // If opp < 70, MEDIUM-risk path catches it; if >= 70, falls through.
        if opp < 70.0 {
            assert!(result.is_some(), "MEDIUM risk + score < 70 should fast-path");
        } else {
            // In [70, 80) → LLM gate should catch it, not fast-path.
            assert!(result.is_none(), "Truly ambiguous MEDIUM + score [70, 80) should fall through to LLM gate");
        }
    }

    // ── opportunity_score tests ───────────────────────────────────────

    #[test]
    fn test_opportunity_score_perfect_bull_above_80() {
        let data = mk_data(0.95, 9.0, 9.0, 9.0, 9.0, 2.0, 0.8, 0.1, 0.0);
        let opp = opportunity_score(&data);
        assert!(opp >= 80.0, "Perfect bull conditions should score ≥ 80, got {}", opp);
        assert!(opp <= 100.0, "Score should be ≤ 100, got {}", opp);
    }

    #[test]
    fn test_opportunity_score_bear_below_50() {
        let data = mk_data(0.5, -8.0, 2.0, 4.0, 4.0, 7.0, 0.1, 0.7, 0.0);
        let opp = opportunity_score(&data);
        assert!(opp < 50.0, "Bear conditions should score < 50, got {}", opp);
    }

    // ── classify_regime tests ─────────────────────────────────────────

    #[test]
    fn test_classify_regime_danger_states() {
        // Low liquidity + low volume
        let data = mk_data(0.5, 0.0, 2.0, 2.0, 5.0, 5.0, 0.3, 0.3, 0.0);
        assert_eq!(classify_regime(&data), "LOW_LIQUIDITY_DANGER");

        // High volatility
        let data = mk_data(0.5, 0.0, 5.0, 5.0, 5.0, 10.0, 0.3, 0.3, 0.0);
        assert_eq!(classify_regime(&data), "HIGH_VOLATILITY_DANGER");

        // Panic selloff
        let data = mk_data(0.5, -9.0, 5.0, 5.0, 5.0, 8.0, 0.3, 0.3, 0.0);
        assert_eq!(classify_regime(&data), "PANIC_SELLOFF");
    }

    #[test]
    fn test_classify_regime_trending() {
        // TRENDING_BULLISH: trend > 7, vol > 6, breakout > 0.6
        let data = mk_data(0.8, 8.0, 7.0, 7.0, 7.0, 5.0, 0.7, 0.2, 0.0);
        assert_eq!(classify_regime(&data), "TRENDING_BULLISH");

        // TRENDING_BEARISH: trend < -7, vol > 6
        let data = mk_data(0.5, -8.0, 7.0, 7.0, 7.0, 5.0, 0.3, 0.5, 0.0);
        assert_eq!(classify_regime(&data), "TRENDING_BEARISH");
    }

    // ── assess_risk tests ─────────────────────────────────────────────

    #[test]
    fn test_assess_risk_critical_when_many_factors() {
        // Multiple critical conditions → CRITICAL
        let data = mk_data(0.3, -9.0, 2.0, 2.0, 2.0, 10.0, 0.2, 0.8, 0.07);
        let treasury = BtcTreasuryState { btc_growth_7d: -0.10, ..BtcTreasuryState::default() };
        let (level, warnings) = assess_risk(&data, &treasury, 4);
        assert_eq!(level, "CRITICAL");
        assert!(!warnings.is_empty());
    }

    #[test]
    fn test_assess_risk_low_when_clean() {
        let data = mk_data(0.90, 5.0, 7.0, 8.0, 8.0, 4.0, 0.5, 0.2, 0.0);
        let treasury = BtcTreasuryState::default();
        let (level, _warnings) = assess_risk(&data, &treasury, 0);
        assert_eq!(level, "LOW");
    }
}

