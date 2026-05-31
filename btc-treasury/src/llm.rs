use serde::Deserialize;

use crate::models::FullBtcAdvisory;

pub struct LlmClient {
    url: String,
    model: String,
    api_key: String,
}

#[derive(Debug, Deserialize)]
struct LlmChoice {
    message: LlmMessage,
}

#[derive(Debug, Deserialize)]
struct LlmMessage {
    content: String,
}

#[derive(Debug, Deserialize)]
struct LlmResponse {
    choices: Vec<LlmChoice>,
}

#[derive(Debug, Deserialize)]
struct AdvisoryJson {
    recommendation: String,
    confidence: f64,
    risk_level: String,
    treasury_mode: String,
    reason: String,
    #[serde(default)]
    warnings: Vec<String>,
    market_regime: String,
    opportunity_score: f64,
    #[serde(default)]
    dynamic_take_profit: f64,
    #[serde(default)]
    dynamic_stop_loss: f64,
    #[serde(default)]
    tp_reason: String,
    #[serde(default)]
    sl_reason: String,
}

impl LlmClient {
    pub fn new(url: String, model: String, api_key: String) -> Self {
        Self { url, model, api_key }
    }

    pub async fn call(&self, system_prompt: &str, user_prompt: &str) -> anyhow::Result<FullBtcAdvisory> {
        let body = serde_json::json!({
            "model": self.model,
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_prompt}
            ],
            "temperature": 0.373
        });

        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(20))
            .build()?;

        let url = format!("{}/chat/completions", self.url);
        let resp = client
            .post(&url)
            .header("Content-Type", "application/json")
            .header("Authorization", format!("Bearer {}", self.api_key))
            .json(&body)
            .send()
            .await?;

        if !resp.status().is_success() {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            anyhow::bail!("LLM API returned {}: {}", status, body);
        }

        let llm: LlmResponse = resp.json().await?;
        let content = llm
            .choices
            .first()
            .ok_or_else(|| anyhow::anyhow!("no choices in LLM response"))?
            .message
            .content
            .clone();

        let result: AdvisoryJson = serde_json::from_str(&content)?;

        Ok(FullBtcAdvisory {
            recommendation: result.recommendation,
            confidence: result.confidence,
            risk_level: result.risk_level,
            treasury_mode: result.treasury_mode,
            reason: result.reason,
            warnings: result.warnings,
            market_regime: result.market_regime,
            opportunity_score: result.opportunity_score,
            bypass_quant: true,
            timestamp: chrono::Utc::now().to_rfc3339(),
            dynamic_take_profit: result.dynamic_take_profit,
            dynamic_stop_loss: result.dynamic_stop_loss,
            tp_reason: result.tp_reason,
            sl_reason: result.sl_reason,
        })
    }
}
