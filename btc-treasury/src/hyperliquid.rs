use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use k256::ecdsa::SigningKey;
use serde::{Deserialize, Serialize};
use sha2::Digest;
use signature::Signer;

use crate::crypto;
use crate::models::BtcMarketData;

const BASE_URL: &str = "https://api.hyperliquid.xyz";

pub struct HyperliquidClient {
    base_url: String,
    api_key: String,
    signing_key: SigningKey,
    client: reqwest::Client,
}

#[derive(Debug, Serialize)]
struct InfoRequest {
    #[serde(rename = "type")]
    ty: String,
}

#[derive(Debug, Serialize)]
struct OrderRequest {
    #[serde(rename = "type")]
    ty: String,
    cls: String,
    ord: OrderSpec,
}

#[derive(Debug, Serialize)]
struct OrderSpec {
    #[serde(rename = "type")]
    ty: String,
    sz: String,
    px: String,
    side: String,
    sym: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    cloid: Option<String>,
}

#[derive(Debug, Serialize)]
struct CancelRequest {
    #[serde(rename = "type")]
    ty: String,
    orders: Vec<CancelSpec>,
}

#[derive(Debug, Serialize)]
struct CancelSpec {
    #[serde(rename = "type")]
    ty: String,
    oid: i64,
    sym: String,
}

#[derive(Debug, Deserialize)]
struct InfoResponse {
    #[serde(default)]
    #[serde(rename = "clearinghouse")]
    clearinghouse: Option<ClearinghouseState>,
    #[serde(default)]
    #[serde(rename = "openOrders")]
    open_orders: Option<Vec<HyperliquidOrder>>,
}

#[derive(Debug, Deserialize)]
struct ClearinghouseState {
    #[serde(default)]
    #[serde(rename = "account")]
    account: Option<AccountBalances>,
    #[serde(default)]
    #[serde(rename = "positions")]
    positions: Option<Vec<HyperliquidPosition>>,
}

#[derive(Debug, Deserialize)]
struct AccountBalances {
    #[serde(default)]
    #[serde(rename = "balances")]
    balances: Option<Vec<BalanceInfo>>,
}

#[derive(Debug, Deserialize)]
struct BalanceInfo {
    #[serde(rename = "coin")]
    coin: String,
    #[serde(rename = "total")]
    total: String,
    #[serde(rename = "hold")]
    hold: String,
    #[serde(rename = "locked")]
    locked: String,
}

#[derive(Debug, Deserialize)]
struct HyperliquidPosition {
    #[serde(rename = "coin")]
    coin: String,
    #[serde(rename = "size")]
    size: String,
    #[serde(rename = "entryPx")]
    entry_px: Option<String>,
    #[serde(rename = "unrealizedPnl")]
    unrealized_pnl: Option<String>,
    #[serde(rename = "marginUsed")]
    margin_used: Option<String>,
    #[serde(rename = "leverage")]
    leverage: Option<serde_json::Value>,
}

#[derive(Debug, Deserialize)]
pub struct HyperliquidOrder {
    #[serde(rename = "oid")]
    pub oid: i64,
    #[serde(rename = "side")]
    pub side: String,
    #[serde(rename = "sz")]
    pub sz: String,
    #[serde(rename = "price")]
    pub price: String,
    #[serde(rename = "symbol")]
    pub symbol: String,
    #[serde(rename = "status")]
    pub status: String,
    #[serde(rename = "filled")]
    pub filled: String,
}

#[derive(Debug, Deserialize)]
pub struct OrderResult {
    #[serde(rename = "type")]
    pub ty: String,
    #[serde(rename = "orderId")]
    pub order_id: Option<i64>,
    #[serde(rename = "status")]
    pub status: String,
    #[serde(rename = "error")]
    pub error: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct CancelResult {
    #[serde(rename = "type")]
    pub ty: String,
    #[serde(rename = "status")]
    pub status: String,
}

#[derive(Debug, Deserialize)]
struct MarketSnapshot {
    #[serde(rename = "coin")]
    coin: String,
    #[serde(rename = "markPx")]
    mark_px: Option<String>,
    #[serde(rename = "prevDayPx")]
    prev_day_px: Option<String>,
    #[serde(rename = "openInterest")]
    open_interest: Option<String>,
    #[serde(rename = "volume")]
    volume: Option<String>,
    #[serde(rename = "highPx")]
    high_px: Option<String>,
    #[serde(rename = "lowPx")]
    low_px: Option<String>,
    #[serde(rename = "lastSz")]
    last_sz: Option<String>,
}

impl HyperliquidClient {
    pub fn new(api_key: String, api_secret: String, base_url: Option<String>) -> Self {
        let secret_bytes = Self::parse_secret(&api_secret);
        let signing_key = SigningKey::from_bytes(&secret_bytes.into()).expect("valid secp256k1 key");
        Self {
            base_url: base_url.unwrap_or_else(|| BASE_URL.to_string()),
            api_key,
            signing_key,
            client: reqwest::Client::new(),
        }
    }

    /// Load credentials from an AES-256-GCM encrypted file.
    /// The file format mirrors executor-ts: salt(16) || iv(12) || tag(16) || ciphertext
    /// The decrypted content is expected to be the private key hex (64 chars).
    /// The wallet address is derived from the private key automatically.
    /// Returns (api_key / wallet address, HyperliquidClient).
    pub fn load_from_encrypted_file(
        enc_path: &Path,
        password: &str,
        base_url: Option<String>,
    ) -> Result<(String, Self)> {
        let encrypted_data = std::fs::read(enc_path)
            .with_context(|| format!("failed to read {}", enc_path.display()))?;

        let secret_hex = crypto::decrypt(&encrypted_data, password)
            .with_context(|| format!("failed to decrypt {} (wrong password?)", enc_path.display()))?;

        let api_key = Self::derive_address_from_secret(secret_hex.trim());
        let client = Self::new(api_key.clone(), secret_hex.trim().to_string(), base_url);

        Ok((api_key, client))
    }

    /// Derive Ethereum-style wallet address (0x...) from a secp256k1 private key.
    pub fn derive_address_from_secret(secret_hex: &str) -> String {
        let secret_bytes = Self::parse_secret(secret_hex);
        let signing_key = SigningKey::from_bytes(&secret_bytes.into()).expect("valid secp256k1 key");
        let verifying_key: &k256::ecdsa::VerifyingKey = signing_key.verifying_key();
        let encoded = verifying_key.to_encoded_point(false);
        // Uncompressed: 0x04 || x || y  (65 bytes). Keccak256 of last 64 bytes → last 20 bytes.
        let hash = sha3::Keccak256::digest(encoded.as_bytes());
        format!("0x{}", hex::encode(&hash[12..]))
    }

    fn parse_secret(secret: &str) -> [u8; 32] {
        let secret = secret.trim();
        let bytes = if secret.len() == 64 {
            hex::decode(secret).unwrap_or_else(|_| secret.as_bytes().to_vec())
        } else {
            secret.as_bytes().to_vec()
        };
        let mut key_bytes = [0u8; 32];
        key_bytes[..bytes.len().min(32)].copy_from_slice(&bytes[..bytes.len().min(32)]);
        key_bytes
    }

    fn sign(&self, msg: &str) -> String {
        let msg_hash = sha2::Sha256::digest(msg.as_bytes());
        let sig: k256::ecdsa::Signature = self.signing_key.sign(&msg_hash);
        hex::encode(sig.to_bytes())
    }

    fn timestamp_ms() -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_millis() as u64
    }

    async fn signed_post<T: serde::de::DeserializeOwned>(&self, path: &str, body: &str) -> Result<T> {
        let ts = Self::timestamp_ms();
        let sign_str = format!("{}{}{}", path, body, ts);
        let signature = self.sign(&sign_str);

        let url = format!("{}{}", self.base_url, path);
        let resp = self
            .client
            .post(&url)
            .header("Content-Type", "application/json")
            .header("X-API-KEY", &self.api_key)
            .header("X-SIGNATURE", &signature)
            .header("X-TIMESTAMP", &ts.to_string())
            .body(body.to_string())
            .send()
            .await?
            .error_for_status()?;
        let body_text = resp.text().await?;
        serde_json::from_str(&body_text)
            .with_context(|| format!("Hyperliquid deserialize error for {}: {}", path, &body_text[..body_text.len().min(300)]))
    }

    async fn public_get<T: serde::de::DeserializeOwned>(&self, path: &str, query: &str) -> Result<T> {
        let url = if query.is_empty() {
            format!("{}{}", self.base_url, path)
        } else {
            format!("{}{}?{}", self.base_url, path, query)
        };
        let resp = self.client.get(&url).send().await?.error_for_status()?;
        let body = resp.text().await?;
        serde_json::from_str(&body)
            .with_context(|| format!("Hyperliquid public error for {}: {}", path, &body[..body.len().min(300)]))
    }

    pub fn api_key_display(&self) -> String {
        if self.api_key.len() > 8 {
            format!("{}...{}", &self.api_key[..4], &self.api_key[self.api_key.len() - 4..])
        } else {
            "***".to_string()
        }
    }

    /// Fetch account state: balances + positions via /info
    pub async fn get_account_state(&self) -> Result<InfoResponse> {
        let body = serde_json::to_string(&InfoRequest {
            ty: "clearinghouseInfo".to_string(),
        })?;
        self.signed_post("/info",&body).await
    }

    /// Get all balances (USDC + any coin with balance)
    pub async fn get_balances(&self) -> Result<Vec<HyperliquidBalance>> {
        let state = self.get_account_state().await?;
        let mut balances = Vec::new();

        if let Some(ch) = state.clearinghouse {
            if let Some(acc) = ch.account {
                if let Some(bals) = acc.balances {
                    for b in bals {
                        let total: f64 = b.total.parse().unwrap_or(0.0);
                        let hold: f64 = b.hold.parse().unwrap_or(0.0);
                        if total > 0.0 || hold > 0.0 {
                            balances.push(HyperliquidBalance {
                                coin: b.coin,
                                total,
                                hold,
 });
                        }
                    }
                }
            }
        }

        Ok(balances)
    }

    /// Get open orders across all pairs
    pub async fn get_open_orders(&self) -> Result<Vec<HyperliquidOrder>> {
        let body = serde_json::to_string(&InfoRequest {
            ty: "openOrders".to_string(),
        })?;
        let resp: InfoResponse = self.signed_post("/info",&body).await?;
        Ok(resp.open_orders.unwrap_or_default())
    }

    /// Get open positions
    pub async fn get_positions(&self) -> Result<Vec<HyperliquidPosition>> {
        let state = self.get_account_state().await?;
        Ok(state
            .clearinghouse
            .and_then(|ch| ch.positions)
            .unwrap_or_default())
    }

    /// Get market data for a perpetual pair (e.g. "BTC-PERP")
    pub async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData> {
        let meta = self.get_meta_and_universe().await?;
        let snapshot = self.get_perpetual_snapshot(symbol).await?;

        let mark_px: f64 = snapshot.mark_px.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);
        let prev_px: f64 = snapshot.prev_day_px.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);
        let high_px: f64 = snapshot.high_px.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);
        let low_px: f64 = snapshot.low_px.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);
        let volume_24h: f64 = snapshot.volume.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);
        let oi: f64 = snapshot.open_interest.as_ref().and_then(|s| s.parse().ok()).unwrap_or(0.0);

        let pct_change = if prev_px > 0.0 {
            (mark_px - prev_px) / prev_px * 100.0
        } else {
            0.0
        };

        let spread = if high_px > 0.0 && low_px > 0.0 {
            (high_px - low_px) / mark_px * 100.0
        } else {
            0.0
        };

        let volatility_score = (spread * 10.0).clamp(0.0, 10.0);
        let volume_score = (volume_24h / 50_000_000.0).min(10.0).max(0.0);
        let liquidity_score = (oi / 100_000_000.0).min(10.0).max(0.0);
        let spread_score = (10.0 - spread * 10.0).clamp(0.0, 10.0);

        let trend_strength = pct_change * 1.0;
        let breakout_probability = if pct_change.abs() > 3.0 && volume_score > 5.0 {
            0.65
        } else {
            0.3
        };
        let reversal_probability = if pct_change.abs() > 6.0 { 0.5 } else { 0.2 };

        let confidence = if liquidity_score > 6.0 && spread_score > 6.0 {
            0.75
        } else {
            0.5
        };

        Ok(BtcMarketData {
            pair: symbol.to_string(),
            market_regime: String::new(),
            trend_strength,
            volume_score,
            liquidity_score,
            spread_score,
            volatility_score,
            breakout_probability,
            reversal_probability,
            confidence,
            active_strategy: "perp_accumulation".into(),
            portfolio_exposure: 0.0,
            daily_drawdown: 0.0,
        })
    }

    async fn get_meta_and_universe(&self) -> Result<serde_json::Value> {
        #[derive(Serialize)]
        struct Req { #[serde(rename = "type")] ty: String }
        let body = serde_json::to_string(&Req { ty: "meta".to_string() })?;
        self.public_get("/info", &format!("type=meta")).await
    }

    async fn get_perpetual_snapshot(&self, symbol: &str) -> Result<MarketSnapshot> {
        #[derive(Serialize)]
        struct Req {
            #[serde(rename = "type")]
            ty: String,
            #[serde(rename = "coin")]
            coin: String,
        }
        let body = serde_json::to_string(&Req {
            ty: "perpMeta".to_string(),
            coin: symbol.replace("-PERP", ""),
        })?;
        #[derive(Deserialize)]
        struct Resp {
            #[serde(rename = "pool")]
            #[serde(default)]
            pool: Option<MarketSnapshot>,
        }
        let resp: Resp = self.signed_post("/info", &body).await?;
        resp.pool.context("no pool data in perpMeta response")
    }

    /// Place a market buy order (size in BTC base units)
    pub async fn place_market_buy(&self, symbol: &str, size: f64) -> Result<OrderResult> {
        let body = serde_json::to_string(&OrderRequest {
            ty: "order".to_string(),
            cls: "market".to_string(),
            ord: OrderSpec {
                ty: "mkt".to_string(),
                sz: format!("{:.8}", size),
                px: "".to_string(),
                side: "Buy".to_string(),
                sym: symbol.to_string(),
                cloid: None,
            },
        })?;
        self.signed_post("/order", &body).await
    }

    /// Place a limit buy order
    pub async fn place_limit_buy(&self, symbol: &str, size: f64, price: f64) -> Result<OrderResult> {
        let body = serde_json::to_string(&OrderRequest {
            ty: "order".to_string(),
            cls: "limit".to_string(),
            ord: OrderSpec {
                ty: "limit".to_string(),
                sz: format!("{:.8}", size),
                px: format!("{:.2}", price),
                side: "Buy".to_string(),
                sym: symbol.to_string(),
                cloid: None,
            },
        })?;
        self.signed_post("/order", &body).await
    }

    /// Cancel an order by oid
    pub async fn cancel_order(&self, symbol: &str, oid: i64) -> Result<CancelResult> {
        let body = serde_json::to_string(&CancelRequest {
            ty: "cancel".to_string(),
            orders: vec![CancelSpec {
                ty: "cancel".to_string(),
                oid,
                sym: symbol.to_string(),
            }],
        })?;
        self.signed_post("/order", &body).await
    }

    /// Cancel all open orders
    pub async fn cancel_all(&self) -> Result<Vec<CancelResult>> {
        let body = serde_json::to_string(&serde_json::json!({
            "type": "cancelAll",
            "all": true
        }))?;
        #[derive(Deserialize)]
        struct Resp {
            #[serde(rename = "type")]
            ty: String,
            #[serde(rename = "status")]
            status: String,
        }
        let resp: Resp = self.signed_post("/order", &body).await?;
        Ok(vec![CancelResult {
            ty: resp.ty,
            status: resp.status,
        }])
    }

    /// Validate if a perpetual symbol exists
    pub async fn validate_symbol(&self, symbol: &str) -> Result<bool> {
        #[derive(Serialize)]
        struct Req { #[serde(rename = "type")] ty: String }
        let body = serde_json::to_string(&Req { ty: "allMids".to_string() })?;
        #[derive(Deserialize)]
        struct Resp { #[serde(default)] #[serde(rename = "midpoint")] midpoint: serde_json::Value }
        let resp: Resp = self.signed_post("/info", &body).await?;
        let Some(map) = resp.midpoint.as_object() else {
            return Ok(false);
        };
        Ok(map.contains_key(symbol))
    }
}

#[derive(Debug, Clone)]
pub struct HyperliquidBalance {
    pub coin: String,
    pub total: f64,
    pub hold: f64,
}
