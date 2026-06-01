use std::sync::Arc;

use crate::llm::LlmClient;
use crate::memory::MemoryStore;
use crate::models::*;

pub struct AdvisoryEngine {
    llm: LlmClient,
    mem: Arc<MemoryStore>,
    skills_context: String,
}

const SYSTEM_PROMPT: &str = r#"You are an autonomous BTC Treasury Accumulation AI.

ROLE: AI Quant Trader, Crypto Portfolio Manager, and BTC Treasury Manager.

CORE OBJECTIVE: Meningkatkan jumlah BTC secara konsisten.
Keberhasilan sistem hanya diukur dari: Δ BTC (BTC gained, bukan USD profit).

Target: BTC(t+1) > BTC(t) — setiap trade harus menambah BTC holdings.

TRADING PHILOSOPHY:
- Semua posisi: SPOT MARKET ONLY — no leverage, no futures, no perpetual.
- Universe: BTC-quote pairs (ETHBTC, SOLBTC, SUIBTC, ADABTC, LINKBTC, dll)
- Score > 80 → AMBIL POSISI. Score < 80 → DO NOTHING. Cash is a position.
- Maksimum 1 posisi aktif.
- Maksimum 1% risiko per trade (after fees).
- Take Profit: 3-8%. Stop Loss: 1-2%.
- TP > |SL| — selalu maintain positive expected value per trade.

FEE-AWARE STOP-LOSS:
- Taker fee 0.1% per trade → round-trip fee 0.2%.
- SL must absorb fee: effective_loss = |SL%| + 0.2%.
- So if config says max 1% risk, set SL at -0.8% → actual max loss = 0.8% + 0.2% = 1.0%.
- For small capital (<$100): set wider TP-to-SL ratio (at least 3:1) to survive fees.
- SL minimum depth: at least 0.5% below entry after fees (0.7% absolute SL).

DYNAMIC TP/SL (set based on market regime + volatility):
When recommending APPROVE, you MUST set conservative yet profitable levels:

- CALM/RANGING market (volatility low, tight spread):
  TP: 2.5-4%, SL: -0.8% to -1.2%  (smaller moves, tighter SL)

- TRENDING market (momentum, breakout):
  TP: 4-7%, SL: -1.2% to -1.8%  (ride trend, wider room)

- VOLATILE market (high ATR, wide swing):
  TP: 6-10%, SL: -1.8% to -2.5%  (need wider targets to survive swings)

- For positions with small BTC capital (≤0.001 BTC):
  Use upper TP range and minimum SL depth (wider TP/SL) — fees eat smaller percentage moves faster.

TP/SL RATIONALE:
- dynamic_take_profit: 3.0 to 10.0 (percentage)
- dynamic_stop_loss: -0.7 to -2.5 (negative percentage, always considering 0.2% fee)
- tp_reason: specific to regime, price level, and volatility
- sl_reason: must mention fee consideration + support/resistance level
- IMPORTANT: The system will automatically widen your SL if it's tighter than 1.5× ATR(14) — ATR is the average 15m noise range. Choose the regime-appropriate range above and the clamp will protect each trade from random noise.
- When ATR is high (>3%), your SL at -0.7% will be automatically widened to ~-3.0% — the TP will scale up proportionally to maintain positive expectancy.

ENTRY CONDITIONS (semua harus terpenuhi):
- RS (Relative Strength) Rising: coin outperform BTC
- EMA20 > EMA50 > EMA200 (bullish alignment)
- MACD Bullish (MACD line > Signal line)
- Volume > Average (volume spike / expansion)

EXIT CONDITIONS:
- Take Profit: dynamic TP based on regime
- Trailing Stop: aktif (track highest price)
- Stop Loss: dynamic SL (hard limit, fee-aware)

ANTI-FOMO RULES:
- Dilarang: Martingale, Averaging Down, Revenge Trading, YOLO Trade, All-In
- 3 loss berturut-turut → Pause Trading 24 Jam
- Drawdown > 10% → Reduce Position Size 50%

TREASURY MANAGEMENT:
- Profit distribution: 50% Compound (trading capital), 50% BTC Treasury Vault
- BTC Treasury Vault tidak boleh digunakan untuk trading
- Selalu hitung BTC accounting:
  {
    "btc_before": "0.00100000",
    "btc_after": "0.00102500",
    "btc_gain": "0.00002500"
  }

AI SCORING MODEL (untuk ranking pair):
- 40% Relative Strength (RS vs BTC across timeframes)
- 25% Volume Growth (spike, expansion, liquidity)
- 20% Trend Strength (EMA alignment, MACD, momentum)
- 10% Volatility Quality (ATR% — enough to capture TP, not too dangerous)
- 5% Market Structure (spread, orderbook depth)

POSSIBLE RECOMMENDATIONS: REJECT, MONITOR, APPROVE, REDUCE_EXPOSURE, EXIT_POSITION, PROTECT_TREASURY, ENABLE_SAFE_MODE.

STRICT PROHIBITIONS:
- Never predict exact prices or future candles
- Never guarantee profits
- Never recommend martingale, revenge trading, all-in, leverage
- Never recommend futures/perpetual trading
- Never measure success in USD

ALWAYS OUTPUT VALID JSON. NO MARKDOWN. NO TEXT OUTSIDE JSON.

Required output structure:
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
  "tp_reason": "TRENDING regime - 5.5% TP captures momentum move above 4h resistance",
  "sl_reason": "1.2% SL + 0.2% fee = 1.4% max loss, below 1.5% support level on 15m"
}"#;


impl AdvisoryEngine {
    pub fn new(llm_url: String, llm_model: String, llm_api_key: String, mem: Arc<MemoryStore>) -> Self {
        let skills = mem.load_skills();
        tracing::info!("BTC advisor: loaded SKILL.md ({} chars)", skills.len());
        Self {
            llm: LlmClient::new(llm_url, llm_model, llm_api_key),
            mem,
            skills_context: skills,
        }
    }

    pub async fn analyze(&self, input: &BtcAdvisoryInput) -> FullBtcAdvisory {
        let config = self.mem.get_config();
        let market_regime = classify_regime(&input.market_data);
        let (risk_level, warnings) = assess_risk(&input.market_data, &input.treasury, input.loss_streak);
        let treasury_mode = treasury_mode(&input.market_data, &input.treasury, &risk_level);

        // Blend AI score with orderbook-based opportunity score
        let orderbook_score = opportunity_score(&input.market_data);
        let opportunity_score = match input.ai_score {
            Some(ai) if ai > 0.0 => {
                let ai_weight = 0.6;
                let ob_weight = 0.4;
                (ai * ai_weight + orderbook_score * ob_weight).round().clamp(0.0, 100.0)
            }
            _ => orderbook_score,
        };

        let should_activate = should_activate_llm(&input.market_data, &input.treasury, input.loss_streak, &config);

        if should_activate && config.enabled {
            tracing::info!("BTC Advisory: activating LLM");
            match self.call_llm(input, &market_regime, &risk_level, &warnings, opportunity_score, &treasury_mode).await {
                Ok(mut advisory) => {
                    advisory.opportunity_score = opportunity_score;
                    advisory.market_regime = market_regime;
                    advisory.bypass_quant = true;
                    return advisory;
                }
                Err(e) => {
                    tracing::error!("BTC LLM call failed: {}", e);
                }
            }
        }

        quant_advisory(&input.market_data, &market_regime, &risk_level, &warnings, opportunity_score, &treasury_mode, config.taker_fee_pct)
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
        let warnings_json = serde_json::to_string(warnings).unwrap_or_default();
        let positions_json = serde_json::to_string(&input.open_positions).unwrap_or_default();

        let ai_score_line = input.ai_score.map(|s| format!("AI Technical Score: {:.1}\n", s)).unwrap_or_default();
        let pair_metrics_json = input.pair_metrics.as_ref()
            .map(|pm| serde_json::to_string(pm).unwrap_or_default())
            .unwrap_or_default();

        let user_prompt = format!(
            r#"CURRENT STATE:
Pair: {}
Market Regime (quant): {}
Trend Strength: {:.1}
Volume Score: {:.1}
Liquidity Score: {:.1}
Spread Score: {:.1}
Volatility Score: {:.1}
Breakout Probability: {:.2}
Reversal Probability: {:.2}
Quant Confidence: {:.2}
Active Strategy: {}
Portfolio Exposure: {:.2}
Daily Drawdown: {:.4}
Taker Fee: {:.2}% (round-trip: {:.2}%)
{}TECHNICAL INDICATORS:
{}

TREASURY:
Current BTC: {:.8}
Previous BTC: {:.8}
7-Day Growth: {:.4}

OPEN POSITIONS:
{}

Loss Streak: {}

QUANT PRELIMINARY:
Risk Level: {}
Treasury Mode: {}
Opportunity Score: {:.0}
Warnings: {}"#,
            input.market_data.pair,
            regime,
            input.market_data.trend_strength,
            input.market_data.volume_score,
            input.market_data.liquidity_score,
            input.market_data.spread_score,
            input.market_data.volatility_score,
            input.market_data.breakout_probability,
            input.market_data.reversal_probability,
            input.market_data.confidence,
            input.market_data.active_strategy,
            input.market_data.portfolio_exposure,
            input.market_data.daily_drawdown,
            config.taker_fee_pct * 100.0,
            config.taker_fee_pct * 200.0,
            ai_score_line,
            pair_metrics_json,
            input.treasury.current_btc,
            input.treasury.previous_btc,
            input.treasury.btc_growth_7d,
            positions_json,
            input.loss_streak,
            risk_level,
            treasury_mode,
            opportunity_score,
            warnings_json,
        );

        let lessons_ctx = self.mem.load_lessons_context();

        let combined_system = if self.skills_context.is_empty() && lessons_ctx.is_empty() {
            SYSTEM_PROMPT.to_string()
        } else {
            format!("{}{}{}", SYSTEM_PROMPT, self.skills_context, lessons_ctx)
        };

        tracing::debug!("BTC LLM system prompt length: {} chars", combined_system.len());
        self.llm.call(&combined_system, &user_prompt).await
    }
}

// ── Quant Functions ──────────────────────────────────────────────────────

fn classify_regime(data: &BtcMarketData) -> String {
    if data.liquidity_score < 3.0 && data.volume_score < 3.0 {
        return "LOW_LIQUIDITY_DANGER".into();
    }
    if data.volatility_score > 9.0 {
        return "HIGH_VOLATILITY_DANGER".into();
    }
    if data.trend_strength > 7.0 && data.volume_score > 6.0 && data.breakout_probability > 0.6 {
        return "TRENDING_BULLISH".into();
    }
    if data.trend_strength < -7.0 && data.volume_score > 6.0 {
        return "TRENDING_BEARISH".into();
    }
    if data.confidence < 0.4 && data.volume_score < 4.0 {
        return "CHOPPY".into();
    }
    if data.breakout_probability > 0.75 && data.trend_strength > 0.0 {
        return "BREAKOUT_EXPANSION".into();
    }
    if data.reversal_probability > 0.75 && data.trend_strength > 5.0 {
        return "FAKE_BREAKOUT".into();
    }
    if data.trend_strength.abs() < 3.0 && data.volume_score > 3.0 {
        return "RANGING".into();
    }
    if data.trend_strength > 3.0 && data.volume_score < 5.0 && data.breakout_probability < 0.35 {
        return "ACCUMULATION".into();
    }
    if data.trend_strength < -3.0 && data.volume_score > 5.0 {
        return "DISTRIBUTION".into();
    }
    if data.trend_strength < -8.0 && data.volatility_score > 7.0 {
        return "PANIC_SELLOFF".into();
    }
    if data.trend_strength.abs() < 2.0 && data.confidence < 0.35 {
        return "CHOPPY".into();
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

    let score = data.liquidity_score * 0.20
        + data.spread_score * 0.10
        + (10.0 - data.volatility_score) * 0.15
        + data.volume_score * 0.15
        + trend_norm * 10.0 * 0.20
        + data.breakout_probability * 10.0 * 0.15
        + (1.0 - data.reversal_probability) * 10.0 * 0.05;

    (score * 100.0).round() / 100.0
}

fn should_activate_llm(data: &BtcMarketData, _treasury: &BtcTreasuryState, loss_streak: i32, cfg: &BtcConfig) -> bool {
    if data.confidence < cfg.llm_activation_threshold {
        return true;
    }
    if data.daily_drawdown > 0.03 {
        return true;
    }
    if loss_streak >= 3 {
        return true;
    }
    if data.volatility_score > cfg.safe_mode_volatility || data.liquidity_score < 4.0 {
        return true;
    }
    if data.confidence < 0.5 {
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
    let round_trip_fee_pct = taker_fee_pct * 200.0; // 0.2%
    // Quant fallback SL: base 0.8% + round-trip fee, capped at 2.0%
    let base_sl = 0.8;
    let quant_sl = -((base_sl + round_trip_fee_pct).clamp(0.8, 2.0));

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
        dynamic_take_profit: 5.5,    // quant fallback: conservative default
        dynamic_stop_loss: quant_sl, // fee-aware: base SL + round-trip fee
        tp_reason: format!("Quant fallback: default 5.5% TP ({:.1}% fee-adjusted SL)", -quant_sl),
        sl_reason: format!("Quant fallback: {:.1}% SL + {:.1}% fee = {:.1}% max loss", base_sl, round_trip_fee_pct, base_sl + round_trip_fee_pct),
    }
}
