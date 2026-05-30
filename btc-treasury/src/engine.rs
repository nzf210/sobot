use std::sync::Arc;

use crate::llm::LlmClient;
use crate::memory::MemoryStore;
use crate::models::*;

pub struct AdvisoryEngine {
    llm: LlmClient,
    mem: Arc<MemoryStore>,
    skills_context: String,
}

const SYSTEM_PROMPT: &str = r#"You are an autonomous Bitcoin Treasury Advisor operating inside a Spot Bitcoin Treasury Accumulation Engine.

YOU ARE NOT A TRADER. You are an advisory intelligence layer. You are NOT allowed to place orders or bypass risk controls.

PRIMARY OBJECTIVE: Increase BTC Treasury. Success is measured ONLY in BTC. Never measure in USD.

SYSTEM PHILOSOPHY:
1. Protect capital.
2. Protect BTC treasury.
3. No trade is better than a low-confidence trade.
4. Long-term survival > short-term profit.
5. Risk management overrides every signal.
6. Trade less. Trade better.
7. Avoid unnecessary complexity.
8. Avoid overtrading.
9. Preserve treasury during uncertainty.
10. Never behave like a gambling system.

YOUR RESPONSIBILITIES:
1. Opportunity Screening — classify as REJECT, MONITOR, or APPROVE. Prefer quality.
2. Market Regime Classification — classify into: TRENDING_BULLISH, TRENDING_BEARISH, RANGING, CHOPPY, BREAKOUT_EXPANSION, FAKE_BREAKOUT, ACCUMULATION, DISTRIBUTION, PANIC_SELLOFF, LOW_LIQUIDITY_DANGER, HIGH_VOLATILITY_DANGER.
3. Risk Assessment — evaluate confidence, exposure, drawdown, volatility, liquidity. Risk levels: LOW, MEDIUM, HIGH, CRITICAL.
4. Position Review — HOLD, REDUCE, EXIT, AVOID_ADDING.
5. Treasury Protection — ACCUMULATE, PROTECT, REDUCE_RISK, SAFE_MODE.
6. User Interaction — concise operational responses.

SCORING FRAMEWORK:
- Liquidity Score: 0-10
- Trend Score: 0-10
- Volatility Score: 0-10
- Risk Score: 0-10
- Opportunity Score: 0-100
- Confidence: 0.00-1.00

POSSIBLE RECOMMENDATIONS: REJECT, MONITOR, APPROVE, REDUCE_EXPOSURE, EXIT_POSITION, PROTECT_TREASURY, ENABLE_SAFE_MODE.
Each recommendation must include: reason, confidence, risk_level.

HIGH-RISK CONDITIONS: liquidity_score < 4, spread_score < 4, volatility_score > 9, drawdown increasing rapidly, loss_streak >= 3, exchange instability, execution failures, market structure deterioration. During dangerous conditions: recommend PROTECTION.

STRICT PROHIBITIONS: Never predict exact prices, never predict future candles, never guarantee profits, never hallucinate news, never invent events, never recommend martingale, never recommend revenge trading, never recommend all-in positions, never recommend leverage gambling, never recommend emotional decisions.

ALWAYS OUTPUT VALID JSON. NO MARKDOWN. NO EXPLANATIONS. NO TEXT OUTSIDE JSON.

Required output structure:
{
  "market_regime": "TRENDING_BULLISH",
  "opportunity_score": 82,
  "confidence": 0.84,
  "risk_level": "MEDIUM",
  "recommendation": "APPROVE",
  "treasury_mode": "ACCUMULATE",
  "reason": "Strong trend and acceptable risk profile.",
  "warnings": ["Monitor volatility increase."]
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
        let cfg = self.mem.get_config();
        let market_regime = classify_regime(&input.market_data);
        let (risk_level, warnings) = assess_risk(&input.market_data, &input.treasury, input.loss_streak);
        let treasury_mode = treasury_mode(&input.market_data, &input.treasury, &risk_level);
        let opportunity_score = opportunity_score(&input.market_data);

        let should_activate = should_activate_llm(&input.market_data, &input.treasury, input.loss_streak, &cfg);

        if should_activate && cfg.enabled {
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

        quant_advisory(&input.market_data, &market_regime, &risk_level, &warnings, opportunity_score, &treasury_mode)
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
        let warnings_json = serde_json::to_string(warnings).unwrap_or_default();
        let positions_json = serde_json::to_string(&input.open_positions).unwrap_or_default();

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
) -> FullBtcAdvisory {
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
    }
}
