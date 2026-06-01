use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use hmac::{Hmac, Mac};
use serde::Deserialize;
use sha2::Sha256;

use crate::models::{BtcMarketData, Ohlcv};

type HmacSha256 = Hmac<Sha256>;

/// Run an async fallible operation with exponential backoff on retryable
/// failures (network errors, 429, 5xx). Non-retryable errors (4xx other than
/// 429) propagate immediately. Up to `MAX_ATTEMPTS` tries, sleeping
/// 1s → 2s → 4s with no jitter — Binance's recommended starting point.
const MAX_ATTEMPTS: u32 = 3;
const BASE_BACKOFF_MS: u64 = 1000;

async fn with_retry<F, Fut, T>(op_name: &str, mut f: F) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = Result<T>>,
{
    let mut last_err: Option<anyhow::Error> = None;
    for attempt in 1..=MAX_ATTEMPTS {
        match f().await {
            Ok(v) => {
                if attempt > 1 {
                    tracing::info!("binance: {} succeeded on attempt {}", op_name, attempt);
                }
                return Ok(v);
            }
            Err(e) => {
                let transient = is_transient_error(&e);
                if !transient || attempt == MAX_ATTEMPTS {
                    if attempt > 1 {
                        tracing::error!(
                            "binance: {} failed after {} attempts: {}",
                            op_name, attempt, e
                        );
                    }
                    return Err(e);
                }
                let backoff_ms = BASE_BACKOFF_MS * (1u64 << (attempt - 1));
                tracing::warn!(
                    "binance: {} failed (attempt {}/{}, transient=true): {} — retrying in {}ms",
                    op_name, attempt, MAX_ATTEMPTS, e, backoff_ms
                );
                tokio::time::sleep(Duration::from_millis(backoff_ms)).await;
                last_err = Some(e);
            }
        }
    }
    Err(last_err.unwrap_or_else(|| anyhow::anyhow!("{}: exhausted retries", op_name)))
}

/// Best-effort detection of retryable errors from reqwest/anyhow. Looks for
/// reqwest's error kind, status code in error chain, or message text.
fn is_transient_error(e: &anyhow::Error) -> bool {
    let chain = e.chain().collect::<Vec<_>>();
    for cause in &chain {
        if let Some(re) = cause.downcast_ref::<reqwest::Error>() {
            if re.is_timeout() || re.is_connect() || re.is_request() {
                return true;
            }
            if let Some(status) = re.status() {
                let s = status.as_u16();
                if s == 429 || (500..600).contains(&s) {
                    return true;
                }
            }
        }
        let msg = cause.to_string().to_lowercase();
        if msg.contains("429") || msg.contains("timeout") || msg.contains("connection") {
            return true;
        }
    }
    false
}

#[derive(Debug, Deserialize)]
pub struct BinanceAccount {
    pub balances: Vec<BinanceBalance>,
}

#[derive(Debug, Deserialize)]
pub struct BinanceBalance {
    pub asset: String,
    pub free: String,
    pub locked: String,
}

#[derive(Debug, Deserialize)]
pub struct BinanceTicker {
    #[serde(default)]
    pub last_price: String,
    #[serde(default)]
    pub price_change_percent: String,
    #[serde(default)]
    pub high_price: String,
    #[serde(default)]
    pub low_price: String,
    #[serde(default)]
    pub volume: String,
    #[serde(default)]
    pub quote_volume: String,
}

#[derive(Debug, Deserialize)]
pub struct BinanceOrder {
    pub symbol: String,
    pub order_id: i64,
    #[serde(default)]
    pub price: String,
    #[serde(default)]
    pub orig_qty: String,
    #[serde(default)]
    pub executed_qty: String,
    #[serde(default)]
    pub side: String,
    #[serde(default)]
    pub status: String,
}

#[derive(Debug, Deserialize)]
pub struct BinanceOrderResult {
    pub symbol: String,
    pub order_id: i64,
    pub status: String,
}

#[derive(Debug, Deserialize)]
pub struct BinanceExchangeInfo {
    pub symbols: Vec<BinanceSymbol>,
}

#[derive(Debug, Deserialize)]
pub struct BinanceSymbol {
    pub symbol: String,
    pub status: String,
}

pub struct BinanceClient {
    base_url: String,
    api_key: String,
    api_secret: String,
    client: reqwest::Client,
}

    // ── OHLCV / Kline data ─────────────────────────────────────────────────────

    #[derive(Debug, Deserialize)]
    struct BinanceKline {
        #[serde(rename = "0")]
        open_time: i64,
        #[serde(rename = "1")]
        open: String,
        #[serde(rename = "2")]
        high: String,
        #[serde(rename = "3")]
        low: String,
        #[serde(rename = "4")]
        close: String,
        #[serde(rename = "5")]
        volume: String,
        #[serde(rename = "6")]
        close_time: i64,
        #[serde(rename = "7")]
        quote_volume: String,
    }



impl BinanceClient {
    pub fn new(api_key: String, api_secret: String, base_url: Option<String>) -> Self {
        Self {
            base_url: base_url.unwrap_or_else(|| "https://api.binance.com".to_string()),
            api_key,
            api_secret,
            client: reqwest::Client::new(),
        }
    }

    fn sign(&self, query: &str) -> String {
        let mut mac = HmacSha256::new_from_slice(self.api_secret.as_bytes())
            .expect("HMAC can take key of any size");
        mac.update(query.as_bytes());
        hex::encode(mac.finalize().into_bytes())
    }

    fn timestamp() -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_millis() as u64
    }

    async fn signed_get<T: serde::de::DeserializeOwned>(&self, path: &str, params: &[(&str, &str)]) -> Result<T> {
        let op = format!("GET {}", path);
        with_retry(&op, || async {
            let ts = Self::timestamp();
            let mut query = format!("timestamp={}", ts);
            for (k, v) in params {
                query.push('&');
                query.push_str(k);
                query.push('=');
                query.push_str(v);
            }
            let signature = self.sign(&query);
            query.push_str("&signature=");
            query.push_str(&signature);

            let url = format!("{}{}?{}", self.base_url, path, query);
            let resp = self.client
                .get(&url)
                .header("X-MBX-APIKEY", &self.api_key)
                .send()
                .await?
                .error_for_status()?;
            let body = resp.text().await?;
            serde_json::from_str(&body)
                .with_context(|| format!("Binance deserialize error for {}: {}", path, &body[..body.len().min(300)]))
        }).await
    }

    async fn signed_post<T: serde::de::DeserializeOwned>(&self, path: &str, params: &[(&str, String)]) -> Result<T> {
        // NOTE: order-placement POSTs are NOT retried — a retry could double-fill
        // a market order. Callers should handle errors and decide whether to retry
        // by checking the order status via a GET.
        let ts = Self::timestamp();
        let mut query = String::new();
        for (k, v) in params {
            if !query.is_empty() { query.push('&'); }
            query.push_str(k);
            query.push('=');
            query.push_str(v);
        }
        query.push_str("&timestamp=");
        query.push_str(&ts.to_string());
        let signature = self.sign(&query);
        query.push_str("&signature=");
        query.push_str(&signature);

        let url = format!("{}{}", self.base_url, path);
        let resp = self.client
            .post(&url)
            .header("X-MBX-APIKEY", &self.api_key)
            .header("Content-Type", "application/x-www-form-urlencoded")
            .body(query)
            .send()
            .await?
            .error_for_status()?;
        let body = resp.text().await?;
        serde_json::from_str(&body)
            .with_context(|| format!("Binance deserialize error for {}: {}", path, &body[..body.len().min(300)]))
    }

    async fn public_get<T: serde::de::DeserializeOwned>(&self, path: &str, query: &str) -> Result<T> {
        let op = format!("GET {}", path);
        with_retry(&op, || async {
            let url = format!("{}{}?{}", self.base_url, path, query);
            let resp = self.client.get(&url).send().await?.error_for_status()?;
            let body = resp.text().await?;
            serde_json::from_str(&body)
                .with_context(|| format!("Binance deserialize error for {}: {}", path, &body[..body.len().min(300)]))
        }).await
    }

    pub fn api_key_display(&self) -> String {
        if self.api_key.len() > 8 {
            format!("{}...{}", &self.api_key[..4], &self.api_key[self.api_key.len()-4..])
        } else {
            "***".to_string()
        }
    }

    pub async fn get_account(&self) -> Result<BinanceAccount> {
        self.signed_get::<BinanceAccount>("/api/v3/account", &[]).await
    }

    pub async fn get_balances(&self) -> Result<Vec<BinanceBalance>> {
        let account = self.get_account().await?;
        Ok(account.balances
            .into_iter()
            .filter(|b| {
                let free: f64 = b.free.parse().unwrap_or(0.0);
                let locked: f64 = b.locked.parse().unwrap_or(0.0);
                free > 0.0 || locked > 0.0
            })
            .collect())
    }

    pub async fn get_orderbook(&self, symbol: &str) -> Result<serde_json::Value> {
        let query = format!("symbol={}&limit=20", symbol);
        self.public_get::<serde_json::Value>("/api/v3/depth", &query).await
    }

    pub async fn get_ticker(&self, symbol: &str) -> Result<BinanceTicker> {
        let query = format!("symbol={}", symbol);
        self.public_get::<BinanceTicker>("/api/v3/ticker/24hr", &query).await
    }

    pub async fn get_market_data(&self, symbol: &str) -> Result<BtcMarketData> {
        let l2 = self.get_orderbook(symbol).await?;

        let bid_levels: Vec<(f64, f64)> = l2["bids"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|l| {
                        let arr = l.as_array()?;
                        Some((arr[0].as_str()?.parse::<f64>().ok()?, arr[1].as_str()?.parse::<f64>().ok()?))
                    })
                    .collect()
            })
            .unwrap_or_default();

        let ask_levels: Vec<(f64, f64)> = l2["asks"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|l| {
                        let arr = l.as_array()?;
                        Some((arr[0].as_str()?.parse::<f64>().ok()?, arr[1].as_str()?.parse::<f64>().ok()?))
                    })
                    .collect()
            })
            .unwrap_or_default();

        let best_bid = bid_levels.first().map(|(p, _)| *p).unwrap_or(0.0);
        let best_ask = ask_levels.first().map(|(p, _)| *p).unwrap_or(0.0);

        let bid_depth: f64 = bid_levels.iter().map(|(_, sz)| sz).sum();
        let ask_depth: f64 = ask_levels.iter().map(|(_, sz)| sz).sum();

        let spread = if best_ask > 0.0 { (best_ask - best_bid) / best_ask * 100.0 } else { 0.0 };

        let volume_score = ((bid_depth + ask_depth) / 100.0).min(10.0);
        let liquidity_score = (bid_depth.min(ask_depth) / 50.0).min(10.0);
        let spread_score = (10.0 - (spread * 100.0).min(10.0)).max(0.0);

        let total_vol = bid_depth + ask_depth;
        let trend_strength = if total_vol > 0.0 {
            (bid_depth - ask_depth) / total_vol * 10.0
        } else {
            0.0
        };

        let confidence = if liquidity_score > 6.0 && spread_score > 6.0 { 0.7 } else { 0.5 };

        let (ticker_change, ticker_vol, ticker_high, ticker_low): (f64, f64, f64, f64) = match self.get_ticker(symbol).await {
            Ok(t) => (
                t.price_change_percent.parse().unwrap_or(0.0),
                t.quote_volume.parse().unwrap_or(0.0),
                t.high_price.parse().unwrap_or(0.0),
                t.low_price.parse().unwrap_or(0.0),
            ),
            Err(_) => (0.0, 0.0, 0.0, 0.0),
        };

        let mid = if best_ask > 0.0 && best_bid > 0.0 { (best_ask + best_bid) / 2.0 } else { 0.0 };
        let volatility_score = if ticker_high > 0.0 && ticker_low > 0.0 {
            ((ticker_high - ticker_low) / mid * 100.0).min(10.0).max(0.0)
        } else {
            5.0
        };

        let ticker_vol_score = (ticker_vol / 50_000_000.0).min(10.0).max(0.0);
        let combined_volume = volume_score * 0.5 + ticker_vol_score * 0.5;

        let breakout_probability = if ticker_change.abs() > 5.0 && combined_volume > 5.0 { 0.65 } else { 0.3 };
        let reversal_probability = if ticker_change.abs() > 8.0 { 0.5 } else { 0.2 };

        Ok(BtcMarketData {
            pair: symbol.to_string(),
            market_regime: String::new(),
            trend_strength,
            volume_score: combined_volume,
            liquidity_score,
            spread_score,
            volatility_score,
            breakout_probability,
            reversal_probability,
            confidence,
            active_strategy: "spot_accumulation".into(),
            portfolio_exposure: 0.0,
            daily_drawdown: 0.0,
        })
    }

    pub async fn get_open_orders(&self, symbol: &str) -> Result<Vec<BinanceOrder>> {
        self.signed_get::<Vec<BinanceOrder>>("/api/v3/openOrders", &[("symbol", symbol)]).await
    }

    pub async fn place_order(
        &self,
        symbol: &str,
        side: &str,
        order_type: &str,
        quantity: f64,
        price: Option<f64>,
    ) -> Result<BinanceOrderResult> {
        let mut params: Vec<(&str, String)> = vec![
            ("symbol", symbol.to_string()),
            ("side", side.to_string()),
            ("type", order_type.to_string()),
            ("quantity", format!("{:.8}", quantity)),
        ];
        if let Some(p) = price {
            if order_type != "MARKET" {
                params.push(("price", format!("{:.2}", p)));
                params.push(("timeInForce", "GTC".to_string()));
            }
        }
        self.signed_post::<BinanceOrderResult>("/api/v3/order", &params).await
    }

    /// Place a MARKET BUY using quoteOrderQty — spend exactly `quote_amount` of quote currency.
    /// This avoids precision loss from mid-price → base quantity conversion.
    pub async fn place_order_quote(
        &self,
        symbol: &str,
        quote_amount: f64,
    ) -> Result<BinanceOrderResult> {
        let params: Vec<(&str, String)> = vec![
            ("symbol", symbol.to_string()),
            ("side", "BUY".to_string()),
            ("type", "MARKET".to_string()),
            ("quoteOrderQty", format!("{:.8}", quote_amount)),
        ];
        self.signed_post::<BinanceOrderResult>("/api/v3/order", &params).await
    }

    pub async fn cancel_order(&self, symbol: &str, order_id: i64) -> Result<BinanceOrderResult> {
        self.signed_delete::<BinanceOrderResult>("/api/v3/order", &[
            ("symbol", symbol),
            ("orderId", &order_id.to_string()),
        ]).await
    }

    pub async fn cancel_all(&self, symbol: &str) -> Result<Vec<BinanceOrderResult>> {
        let orders = self.get_open_orders(symbol).await?;
        let mut results = Vec::new();
        for order in &orders {
            match self.cancel_order(symbol, order.order_id).await {
                Ok(r) => results.push(r),
                Err(e) => {
                    tracing::warn!("Failed to cancel order {}: {}", order.order_id, e);
                }
            }
        }
        Ok(results)
    }

    async fn signed_delete<T: serde::de::DeserializeOwned>(&self, path: &str, params: &[(&str, &str)]) -> Result<T> {
        let ts = Self::timestamp();
        let mut query = String::new();
        for (k, v) in params {
            if !query.is_empty() { query.push('&'); }
            query.push_str(k);
            query.push('=');
            query.push_str(v);
        }
        query.push_str("&timestamp=");
        query.push_str(&ts.to_string());
        let signature = self.sign(&query);
        query.push_str("&signature=");
        query.push_str(&signature);

        let url = format!("{}{}?{}", self.base_url, path, query);
        let resp = self.client
            .delete(&url)
            .header("X-MBX-APIKEY", &self.api_key)
            .send()
            .await?
            .error_for_status()?;
        let body = resp.text().await?;
        serde_json::from_str(&body)
            .with_context(|| format!("Binance deserialize error for {}: {}", path, &body[..body.len().min(300)]))
    }

    pub async fn validate_symbol(&self, symbol: &str) -> Result<bool> {
        match self.public_get::<BinanceExchangeInfo>("/api/v3/exchangeInfo", "").await {
            Ok(info) => Ok(info.symbols.iter().any(|s| s.symbol == symbol && s.status == "TRADING")),
            Err(_) => Ok(false),
        }
    }

    /// Fetch klines (OHLCV) for a symbol and interval.
    /// Returns up to `limit` candles, most recent first.
    pub async fn get_klines(
        &self,
        symbol: &str,
        interval: &str,
        limit: u32,
    ) -> Result<Vec<Ohlcv>> {
        let query = format!("symbol={}&interval={}&limit={}", symbol, interval, limit);
        let data: Vec<Vec<serde_json::Value>> = self.public_get("/api/v3/klines", &query).await?;
        let mut klines = Vec::with_capacity(data.len());
        for row in data {
            if row.len() < 8 { continue; }
            klines.push(Ohlcv {
                open_time: row[0].as_i64().unwrap_or(0),
                open: parse_f64(&row[1]),
                high: parse_f64(&row[2]),
                low: parse_f64(&row[3]),
                close: parse_f64(&row[4]),
                volume: parse_f64(&row[5]),
                quote_volume: parse_f64(&row[7]),
            });
        }
        Ok(klines)
    }

    /// Discover all BTC-quote pairs currently trading on Binance Spot.
    pub async fn discover_btc_pairs(&self) -> Result<Vec<String>> {
        #[derive(Deserialize)]
        struct AllSymbols { symbols: Vec<BinanceSymbol> }
        let info: AllSymbols = self.public_get("/api/v3/exchangeInfo", "").await?;
        let pairs: Vec<String> = info.symbols
            .into_iter()
            .filter(|s| s.symbol.ends_with("BTC") && s.status == "TRADING")
            .map(|s| s.symbol)
            .collect();
        Ok(pairs)
    }

    /// Get current price for a symbol (public endpoint).
    pub async fn get_price(&self, symbol: &str) -> Result<f64> {
        #[derive(Deserialize)]
        struct PriceResp { price: String }
        let query = format!("symbol={}", symbol);
        let resp: PriceResp = self.public_get("/api/v3/ticker/price", &query).await?;
        resp.price.parse::<f64>()
            .map_err(|e| anyhow::anyhow!("parse price error: {}", e))
    }
}

fn parse_f64(v: &serde_json::Value) -> f64 {
    v.as_str()
        .and_then(|s| s.parse::<f64>().ok())
        .unwrap_or(0.0)
}
