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

/// Strip markdown code-fence wrappers that some LLMs add around JSON.
/// For example:
///   ```json\n{...}\n```
///   ```\n{...}\n```
/// are both reduced to just `{...}`.
fn strip_code_fence(s: &str) -> &str {
    let trimmed = s.trim();
    // Check for opening fence like ```json or ```
    if let Some(rest) = trimmed.strip_prefix("```") {
        // Skip the optional language tag and the first newline
        let after_tag = if let Some(nl) = rest.find('\n') {
            &rest[nl + 1..]
        } else {
            rest
        };
        // Remove trailing closing fence
        let cleaned = after_tag.trim_end();
        if let Some(without_fence) = cleaned.strip_suffix("```") {
            return without_fence.trim();
        }
        return after_tag.trim();
    }
    trimmed
}

/// Extract the first JSON object `{...}` from `s`. Some LLM endpoints return
/// extra text before or after the JSON payload even when instructed not to.
fn extract_json_object(s: &str) -> Option<&str> {
    let start = s.find('{')?;
    // Find the matching closing brace (naive — works for flat/single-level JSON)
    let mut depth: i32 = 0;
    let mut end = start;
    for (i, ch) in s[start..].char_indices() {
        match ch {
            '{' => depth += 1,
            '}' => {
                depth -= 1;
                if depth == 0 {
                    end = start + i;
                    break;
                }
            }
            _ => {}
        }
    }
    if depth == 0 && end >= start {
        Some(&s[start..=end])
    } else {
        None
    }
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
            .timeout(std::time::Duration::from_secs(30))
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

        // Read the raw body first so we can log it on parse failure.
        let raw_body = resp.text().await?;
        if raw_body.trim().is_empty() {
            anyhow::bail!("LLM API returned an empty response body");
        }

        // Parse the outer OpenAI-compatible envelope.
        let llm: LlmResponse = serde_json::from_str(&raw_body).map_err(|e| {
            anyhow::anyhow!(
                "Failed to parse LLM envelope JSON: {} — raw (first 500 chars): {}",
                e,
                &raw_body[..raw_body.len().min(500)]
            )
        })?;

        let content = llm
            .choices
            .first()
            .ok_or_else(|| anyhow::anyhow!("no choices in LLM response"))?
            .message
            .content
            .clone();

        if content.trim().is_empty() {
            anyhow::bail!("LLM returned an empty content field");
        }

        // Robustly extract JSON from the content field:
        //   1. Strip any ```json ... ``` code fences.
        //   2. Extract the first JSON object if there is preamble text.
        let stripped = strip_code_fence(&content);
        let json_str = extract_json_object(stripped)
            .or_else(|| extract_json_object(&content))
            .unwrap_or(stripped);

        let result: AdvisoryJson = serde_json::from_str(json_str).map_err(|e| {
            anyhow::anyhow!(
                "Failed to parse AdvisoryJson: {} — content (first 500 chars): {}",
                e,
                &content[..content.len().min(500)]
            )
        })?;

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
