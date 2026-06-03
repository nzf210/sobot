# PLAN: Multi-Account & Multi-CEX (Binance + OKX) untuk btc-treasury

> **Tujuan akhir:** Satu proses btc-treasury bisa menjalankan **N akun** (sub-account/sub-portfolio) di **M CEX** (Binance, OKX, dan CEX tambahan ke depan) secara bersamaan, terisolasi, dapat diawasi via Telegram, dengan backward-compat penuh untuk setup akun-tunggal hari ini.

---

## 0. Inventarisasi kode yang terdampak (hasil pemindaian)

Semua titik yang saat ini mengasumsikan **satu** akun / **satu** CEX:

| Lokasi | Simbol | Status saat ini |
|---|---|---|
| `src/main.rs:42-65` | `exchange_client: Option<Arc<dyn ExchangeClient>>` | tunggal, dari `cfg.exchange_api_key/secret` |
| `src/main.rs:124-131` | `ExecutionEngine` + `scanner::run` | satu instance, satu scanner pool |
| `src/main.rs:140-149` | `PositionMonitor` | satu instance, satu exchange |
| `src/config.rs` | `AppConfig` (exchange_name, exchange_api_key, exchange_api_secret, exchange_base_url) | flat, satu CEX |
| `src/server.rs` | handler `/btc/*` | ambil `shared.exchange` tunggal |
| `src/telegram_bot.rs` | `BtcBot.exchange: Option<Arc<dyn ExchangeClient>>` (line 150) | satu exchange, ada switch-by-id di beberapa command |
| `src/scanner.rs:199` | `exchange: Arc<dyn ExchangeClient>` | satu exchange |
| `src/position_monitor.rs:14` | `exchange: Option<Arc<dyn ExchangeClient>>` | satu exchange |
| `src/execution_engine.rs:12` | `exchange: Option<Arc<dyn ExchangeClient>>` | satu exchange |
| `src/memory.rs` | `MemoryStore { data_dir }` | satu root dir global |
| `src/reporter.rs` | aggregate ke banyak chat_id | sudah multi-chat, tapi single-account |
| `src/engines/*` | tidak tahu CEX | aman |
| `src/indicators.rs`, `src/llm.rs`, `src/format.rs`, `src/sanitize.rs` | tidak tahu CEX | aman |

**Implikasi utama:** trait `ExchangeClient` (di `src/exchange.rs`) sudah ada dan Binance sudah implement — ini pondasi ideal. Yang dibutuhkan adalah **registry per akun** + **dispatcher** yang me-route call ke adapter yang tepat, plus **per-account state**.

---

## 1. Prinsip desain

1. **Backward-compat absolut.** Kalau user cuma set `BINANCE_API_KEY`/`BINANCE_API_SECRET` lama, program jalan persis seperti hari ini, dengan ID akun `default`. Zero breaking change untuk setup 1-akun.
2. **Trait-first, bukan enum.** Trait `ExchangeClient` tetap; tambahkan `OkxClient` sebagai adapter kedua. Dispatcher `MultiExchangeClient` membungkus koleksi adapter + routing.
3. **Account = unit isolasi.** Scanner, PositionMonitor, ExecutionEngine, AdvisoryEngine, MemoryStore, dan Telegram session di-spawn **per akun**. Crash satu akun tidak menjatuhkan akun lain (supervised task).
4. **No global mutable singletons.** Ganti `Arc<MemoryStore>` tunggal dengan `Arc<MemoryStore>` per akun + aggregator view read-only.
5. **Secrets out-of-source.** API key/secret dibaca dari env (per-akun prefix) atau Docker secret. Tidak pernah ditulis ke JSON state.
6. **Rate-limit per exchange, bukan global.** Binance: 1200 req/min IP. OKX: 20 req/2s endpoint, 60 req/s total. Masing-masing adapter mengelola bucket sendiri.
7. **Pair-format awareness.** Binance: `SOLBTC`. OKX: `SOL-BTC` (dash, instType=SPOT). Adapter menormalkan ke `SOLBTC` internal; format eksternal dikonversi saat call.

---

## 2. Skema data: Account & Exchange

### 2.1 Struct `AccountSpec` (config layer)

```rust
// src/config.rs (baru, di samping AppConfig lama)
#[derive(Debug, Clone)]
pub struct AccountSpec {
    pub id: String,                // unik, slug: "main", "okx-alpha"
    pub label: String,             // human label utk Telegram
    pub exchange: ExchangeKind,    // Binance | Okx
    pub credentials: Credentials,  // env-only, lihat 2.3
    pub scanner_pairs: Vec<String>,
    pub telegram_chat_ids: Vec<i64>,  // override; kosong = pakai global
    pub risk: RiskOverrides,       // risk_per_trade_pct, max_positions, daily_loss_limit_btc
    pub enabled: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ExchangeKind { Binance, Okx }

#[derive(Debug, Clone)]
pub enum Credentials {
    EnvKeySecret { key_env: String, secret_env: String, passphrase_env: Option<String> },
    Inline { api_key: String, api_secret: String, passphrase: Option<String> },  // utk tests
}
```

### 2.2 `AccountKey` (runtime routing key)

```rust
#[derive(Debug, Clone, Hash, PartialEq, Eq)]
pub struct AccountKey { pub exchange: ExchangeKind, pub account_id: String }
```

### 2.3 Loader (env-driven, prioritas: JSON env > prefix-per-akun > legacy single)

Urutan resolusi (dari spesifik ke generik):

1. `BTC_ACCOUNTS_JSON` — string JSON, array of accounts. Paling eksplisit, paling mudah di-mount dari secret manager.
2. `BTC_ACCOUNTS_LIST` — comma-separated IDs, lalu baca `BTC_ACC_<ID>_EXCHANGE`, `BTC_ACC_<ID>_API_KEY`, `BTC_ACC_<ID>_API_SECRET`, `BTC_ACC_<ID>_API_PASSPHRASE` (OKX), `BTC_ACC_<ID>_PAIRS`, `BTC_ACC_<ID>_RISK_PER_TRADE_PCT`, `BTC_ACC_<ID>_MAX_POSITIONS`, `BTC_ACC_<ID>_DAILY_LOSS_LIMIT_BTC`, `BTC_ACC_<ID>_ENABLED`, `BTC_ACC_<ID>_TELEGRAM_CHATS`.
3. **Legacy fallback** (backward-compat): kalau tidak ada BTC_ACCOUNTS_JSON dan tidak ada BTC_ACCOUNTS_LIST, buat satu akun `default` dari `BINANCE_API_KEY`/`BINANCE_API_SECRET`/`EXCHANGE_NAME`/`BTC_SCANNER_PAIRS` yang sudah ada. Tepat seperti perilaku lama.

Validasi saat load: id unik, exchange dikenal, credentials ada, enabled=true → tetap dimuat, enabled=false → dimuat tapi tidak di-spawn (bisa diaktifkan via `/btc_enable <id>`).

### 2.4 `RiskOverrides` (per akun)

```rust
#[derive(Debug, Clone, Default)]
pub struct RiskOverrides {
    pub risk_per_trade_pct: Option<f64>,
    pub max_positions: Option<u32>,
    pub daily_loss_limit_btc: Option<f64>,
    pub max_consecutive_losses: Option<u32>,
    pub take_profit_pct: Option<f64>,
    pub stop_loss_pct: Option<f64>,
    pub trailing_tp_pct: Option<f64>,
}
```

Field `None` ⇒ pakai default global (dari `btc-config.json`).

---

## 3. Multi-exchange dispatcher

### 3.1 Trait `ExchangeClient` tetap

Tidak ada perubahan signature — adapter baru (OKX) cukup implement. Ini meminimalkan blast radius.

### 3.2 `MultiExchangeClient` (router)

```rust
// src/multi_exchange.rs (file baru)
pub struct MultiExchangeClient {
    accounts: HashMap<AccountKey, Arc<dyn ExchangeClient>>,
    default_key: AccountKey,
}

impl MultiExchangeClient {
    pub fn new(accounts: HashMap<AccountKey, Arc<dyn ExchangeClient>>, default_key: AccountKey) -> Self;
    pub fn for_account(&self, key: &AccountKey) -> Option<Arc<dyn ExchangeClient>>;
    pub fn default(&self) -> Arc<dyn ExchangeClient>;
    pub fn list(&self) -> Vec<AccountSummary>;
}
```

Pemakaian dari kode existing: ganti `Arc<dyn ExchangeClient>` di semua tempat dengan **`Arc<MultiExchangeClient>`**, dan tambahkan helper `for_account(key)` saat spawn per-akun. Default-nya adalah akun `default` (legacy single-account) sehingga handler yang tidak membawa `?account=` tetap berfungsi.

### 3.3 Layer sign & HTTP terisolasi per adapter

- `BinanceClient` (existing) — tidak berubah strukturnya. Tambah `api_key_id: String` agar log trace.
- `OkxClient` (baru) — `src/okx.rs`. Mirip `BinanceClient` tapi:
  - Base URL: `https://www.okx.com`
  - Signing: ISO8601 timestamp + method (uppercase) + requestPath + body → HMAC-SHA256 → base64. Secret **didecode base64** dulu.
  - Header: `OK-ACCESS-KEY`, `OK-ACCESS-SIGN`, `OK-ACCESS-TIMESTAMP`, `OK-ACCESS-PASSPHRASE` (kalau ada).
  - Endpoints:
    - `GET /api/v5/account/balance` (signed) → map ke `get_balances`
    - `GET /api/v5/market/books?instId=...` → orderbook (untuk `get_current_price` mid)
    - `GET /api/v5/market/ticker?instId=...` → `price_change_percent` dsb
    - `GET /api/v5/market/candles?instId=...&bar=15m&limit=200` → OHLCV
    - `GET /api/v5/market/products` (public) → pair discovery BTC-quote (suffix `-BTC` di instId, instType=SPOT)
    - `POST /api/v5/trade/order` body JSON `{instId, side, ordType, sz, tdMode=cash}` → market buy/sell; `px` ditambah saat limit
  - Pair-format: simpan internal sebagai `SOLBTC`, konversi ke `SOL-BTC` di adapter.
  - **Retry:** sama dengan Binance (`with_retry` di-extract jadi `util::with_retry`).
  - **No-retry POST** untuk trade placement (konsisten dgn catatan `binance.rs:228`).

### 3.4 Rate-limit guard per exchange

Buat `src/rate_limit.rs` (generic):

```rust
pub struct TokenBucket { capacity: u32, refill_per_sec: f64, ... }
impl TokenBucket { pub async fn acquire(&self); }
```

Binance: 1 bucket, 1200/min ⇒ 20/s. OKX: 2 bucket — endpoint-trade (60 req/2s) & endpoint-market (20 req/2s). Adapter memanggil `bucket.acquire().await` sebelum request publik/signed. Tidak menyentuh `with_retry` (HTTP retry adalah orthogonal; ini mencegah *priming* the 429).

---

## 4. Per-account runtime (spawn model)

### 4.1 `AccountRuntime` (struct utama, satu per akun)

```rust
pub struct AccountRuntime {
    pub key: AccountKey,
    pub spec: AccountSpec,
    pub exchange: Arc<dyn ExchangeClient>,
    pub mem: Arc<MemoryStore>,
    pub engine: Arc<AdvisoryEngine>,
    pub executor: Arc<ExecutionEngine>,
    pub position_monitor: Arc<PositionMonitor>,
    pub scanner_state: Arc<ScannerState>,
    pub telegram_session: TelegramSession,  // lihat 5.2
    pub supervisor: JoinHandle<()>,         // restart on panic
}
```

### 4.2 Factory di `main.rs`

```rust
let dispatcher = build_multi_exchange(&cfg)?;   // langkah 3
let accounts = build_account_specs(&cfg, &dispatcher)?;
for spec in accounts.into_iter().filter(|a| a.enabled) {
    let rt = AccountRuntime::spawn(spec.clone(), dispatcher.for_account(&key).unwrap())?;
    runtimes.push(rt);
}
```

`AccountRuntime::spawn` membuat:
- `MemoryStore` baru di `data/btc-treasury/accounts/<id>/` (lihat §5).
- `AdvisoryEngine` baru (LLM client dishare; advisory state per akun).
- `ExecutionEngine` baru dengan `exchange` ter-isolasi.
- `PositionMonitor` baru.
- `ScannerState` baru dengan `scanner_pairs` dari spec.
- Task `scanner::run`, `position_monitor::start`, `reporter::run` (jika telegram override), `supervisor` (watchdog).
- `tokio::spawn` semuanya; simpan `JoinHandle` di struct.

### 4.3 Supervisor pattern (graceful per-account failure)

Setiap spawn dibungkus:

```rust
loop {
    let handle = tokio::spawn(work_loop(rt.clone()));
    match handle.await {
        Ok(_) => break,  // graceful exit
        Err(e) if e.is_panic() => {
            tracing::error!("account {} panicked: {e:?} — restarting in 30s", rt.key);
            sleep(Duration::from_secs(30)).await;
        }
        Err(e) => { tracing::error!("account {} task join error: {e}", rt.key); break; }
    }
}
```

Akun `default` (legacy) **tidak boleh** crash seluruh proses — supervisor-nya sama, tapi `main` tidak propagate panic.

### 4.4 Pair discovery per exchange

- Binance: `discover_btc_pairs()` (sudah ada di `binance.rs:516`).
- OKX: `discover_btc_pairs()` di `okx.rs` — filter `instType=SPOT` dan `instId` suffix `-BTC`; konversi ke format internal `XXXBTC`.
- `/btc_discover` memilih exchange via `?exchange=okx` atau via akun aktif di Telegram.

### 4.5 Scanner pool sizing

- Default 4 worker per akun (sama dengan hari ini).
- Per-process cap: 4 × N_akun worker. Tidak perlu global semaphore kecuali N_akun besar (kemungkinan tidak di fase awal).
- Jika ke depan dibatasi: tambahkan `tokio::sync::Semaphore` di `scanner::run` outer-loop.

---

## 5. State isolation

### 5.1 Layout filesystem

```
data/btc-treasury/
├── SKILL.md                          # shared
├── btc-treasury.json                 # AGGREGATOR VIEW (computed on demand)
├── btc-config.json                   # GLOBAL defaults
├── accounts/
│   ├── default/                      # akun legacy (backward-compat path)
│   │   ├── btc-treasury.json
│   │   ├── btc-config.json           # risk override
│   │   ├── btc-positions.json
│   │   ├── btc-decision-log.json
│   │   ├── btc-lessons.json
│   │   └── SKILL.md (symlink/copy)
│   ├── okx-alpha/
│   │   └── ... (sama)
│   └── binance-sub2/
│       └── ...
```

### 5.2 Backward-compat path

- **Mode lama (env lama, BTC_ACCOUNTS_JSON tidak ada):** tulis file di root lama (`data/btc-treasury/btc-treasury.json` dst.) — supaya `docker-compose` lama tidak kehilangan posisi.
- **Mode baru:** tulis di `accounts/default/`, `accounts/okx-alpha/`, dst. Root berisi `accounts/` saja.
- **Deteksi:** `accounts/.layout_version == 2` ⇒ mode baru; kalau tidak ada ⇒ mode lama.

### 5.3 MemoryStore refactor

`MemoryStore` saat ini hardcode `btc-*.json` filenames. Refactor: ganti method internal jadi `read_json(&self, relpath: &str, default)` + `write_json(&self, relpath, &data)`. Tetapkan konvensi `account_id = None` (default) untuk kompat, dan `Some(id)` untuk per-akun. Method publik `get_treasury()`, `get_config()`, `save_position()`, `record_decision()`, `append_lesson()` tetap, tapi internally resolve path dari `account_id` opsional.

### 5.4 Aggregator view

`GET /btc/aggregate` menjumlahkan lintas akun:
- Total BTC (vault + compound + free)
- Total PnL (24h/7d/30d) — pakai `btc_growth_*` per akun
- Trade count, win rate per akun
- Per-akun status (running / paused / error)

Implementasi: read-only `AggregatorView` struct, baca JSON files on demand (tidak duplikasi state — sumber kebenaran tetap per-akun).

---

## 6. API & Telegram

### 6.1 HTTP (`src/server.rs`)

Tambah query param `?account=ID` ke semua endpoint `/btc/*` kecuali yang memang cross-account (`/btc/aggregate`, `/btc/accounts`).

Daftar endpoint baru:

| Method+Path | Deskripsi |
|---|---|
| `GET /btc/accounts` | List akun + status (running/paused/error/last_heartbeat) |
| `POST /btc/accounts/{id}/enable` | Aktifkan (kalau disabled) |
| `POST /btc/accounts/{id}/disable` | Pause scanner + position monitor |
| `GET /btc/aggregate` | Rollup lintas akun |
| `GET /btc/{action}?account=ID` | Backward-compat — default account bila kosong |

### 6.2 Telegram (`src/telegram_bot.rs`)

Saat ini `BtcBot` punya satu `exchange`. Ubah jadi `accounts: Arc<HashMap<AccountKey, TelegramSession>>` + `active_key: Arc<RwLock<AccountKey>>`.

Command baru / berubah:

| Command | Perubahan |
|---|---|
| `/btc_use <id>` | Switch akun aktif untuk chat ini. Stored di `chat_sessions[chat_id].active_account` |
| `/btc_accounts` | List akun + status |
| `/btc_aggregate` | Lintas akun ringkasan |
| `/btc_disable <id>` | Pause akun tertentu |
| `/btc_enable <id>` | Resume akun tertentu |
| `/btc_status`, `/btc_positions`, `/btc_pairs`, `/btc_advisory`, `/btc_buy`, `/btc_sell`, `/btc_close`, `/btc_closeall`, `/btc_cancel`, `/btc_market`, `/btc_scan`, `/btc_pairinfo`, `/btc_history`, `/btc_lessons`, `/btc_config`, `/btc_setconfig` | Semua menerima `<id>` opsional; default = akun aktif chat |
| `/btc_discover [exchange]` | Tambah param: `binance` (default) atau `okx` |

Backwards-compat: `BtcBot` di-spawn sekali per proses (bukan per akun). Session state (active_account, history size, dll) **per chat_id** (bukan global) — sehingga 2 user beda akun di chat berbeda tidak saling ganggu.

### 6.3 Reporter

Reporter dipecah:
- Per-akun reporter: mengirim ke `spec.telegram_chat_ids` (override) ATAU global `TELEGRAM_REPORT_CHAT_IDS` yang ditandai prefix `[default]`/`[okx-alpha]`.
- Lintas-akun reporter (opsional, opt-in via `BTC_AGGREGATE_REPORT=true`): kirim rollup ke `TELEGRAM_REPORT_CHAT_IDS` global.

---

## 7. docker-compose, env, secrets

### 7.1 `docker-compose.yml` (snippet)

```yaml
btc-treasury:
  build: ./btc-treasury
  environment:
    BTC_ACCOUNTS_JSON: ${BTC_ACCOUNTS_JSON}
    TELEGRAM_BOT_BTC_TOKEN: ${TELEGRAM_BOT_BTC_TOKEN}
    TELEGRAM_REPORT_CHAT_IDS: ${TELEGRAM_REPORT_CHAT_IDS}
    LLM_API_KEY: ${LLM_API_KEY}
    DATA_BTC_DIR: /data/btc-treasury
  volumes:
    - btc-data:/data/btc-treasury
  # healthcheck tetap per proses
```

### 7.2 `.env.example` (tambahan)

```bash
# === Multi-account ===
# Opsi 1: JSON eksplisit (recommended)
BTC_ACCOUNTS_JSON='[
  {"id":"default","label":"Main Binance","exchange":"binance",
   "credentials":{"key_env":"BINANCE_API_KEY","secret_env":"BINANCE_API_SECRET"},
   "scanner_pairs":["ETHBTC","SOLBTC"],"enabled":true},
  {"id":"okx-alpha","label":"OKX Spot Alpha","exchange":"okx",
   "credentials":{"key_env":"OKX_ALPHA_KEY","secret_env":"OKX_ALPHA_SECRET","passphrase_env":"OKX_ALPHA_PASSPHRASE"},
   "scanner_pairs":["ETH-BTC","SOL-BTC"],"enabled":true,
   "risk":{"risk_per_trade_pct":0.5,"max_positions":2}}
]'

# Opsi 2: prefix-based
# BTC_ACCOUNTS_LIST=main,okx-alpha
# BTC_ACC_main_EXCHANGE=binance
# BTC_ACC_main_API_KEY=...
# BTC_ACC_okx-alpha_EXCHANGE=okx
# BTC_ACC_okx-alpha_API_KEY=...
# BTC_ACC_okx-alpha_API_SECRET=...
# BTC_ACC_okx-alpha_API_PASSPHRASE=...
```

### 7.3 Secrets handling

- Production: mount Docker secrets atau gunakan secret manager (HashiCorp Vault, AWS Secrets Manager). `Credentials::EnvKeySecret` membaca file path di env → `std::fs::read_to_string`.
- `wallet_password` & API secret **tidak pernah di-log**. Masking hanya 4-char prefix + 4-char suffix (sudah ada di `binance.rs:269-275`); helper `mask_secret()` di-extract ke `util::secrets`.

### 7.4 Volume per akun (opsional, untuk isolasi ekstra)

```yaml
volumes:
  - btc-data-default:/data/btc-treasury/accounts/default
  - btc-data-okx:/data/btc-treasury/accounts/okx-alpha
```

Default: satu volume shared (lebih mudah backup, cukup untuk 2-5 akun).

---

## 8. Observability & keamanan

- **Logging:** tambah `tracing::Span` field `account_id` & `exchange` di semua loop scanner/monitor/orders. JSON log filter via `RUST_LOG=info,btc_treasury=debug,btc_treasury::okx=debug`.
- **Metrics (light, no new dep):** `data/btc-treasury/accounts/<id>/metrics.json` — rolling counters: orders placed, orders failed, API errors per endpoint, last heartbeat. Format Prometheus-compatible text dump juga OK (`/btc/aggregate?format=prom`).
- **Healthcheck:** `GET /btc/accounts` return 200 + daftar. `docker-compose` healthcheck tetap `GET /health`.
- **Audit:** decision log + lesson log tetap per akun, sudah terisolasi oleh §5.

---

## 9. Tahapan eksekusi (semua additive, reversible)

### Fase 0 — Fondasi trait & registry (zero behavior change)
- [ ] Tambah `ExchangeKind`, `AccountSpec`, `RiskOverrides`, `Credentials` di `src/config.rs` (atau file `src/account_spec.rs`).
- [ ] Tambah loader: `BTC_ACCOUNTS_JSON` → `BTC_ACCOUNTS_LIST` → legacy fallback.
- [ ] Tambah `MultiExchangeClient` di `src/multi_exchange.rs` (HashMap wrapper).
- [ ] Refactor: `main.rs` & `server.rs` & `telegram_bot.rs` ganti tipe `Arc<dyn ExchangeClient>` → `Arc<MultiExchangeClient>`. Default key = `default`.
- [ ] Smoke test: jalankan dgn env lama, harus identik dgn hari ini.

### Fase 1 — Per-account runtime (multi-instance, single CEX dulu)
- [ ] `AccountRuntime::spawn` + supervisor.
- [ ] `MemoryStore` refactor: `account_id: Option<String>` di constructor; path resolution di method internal.
- [ ] `main.rs` loop akun; legacy path = 1 akun `default`.
- [ ] `server.rs`: `?account=ID` param; `/btc/accounts` endpoint.
- [ ] `telegram_bot.rs`: `active_account` per chat; `/btc_use`, `/btc_accounts`, `/btc_aggregate`.
- [ ] `reporter.rs`: per-akun + aggregate.
- [ ] Test: 2 akun Binance (sub-account main + sub-account testnet), scanner paralel, posisi terisolasi.

### Fase 2 — OKX adapter
- [ ] `src/okx.rs`: struct, signing, endpoints.
- [ ] Adapter implement `ExchangeClient` trait.
- [ ] Pair format conversion `SOLBTC` ↔ `SOL-BTC`.
- [ ] Rate-limit `TokenBucket` (60/2s trade, 20/2s market).
- [ ] Pair discovery BTC-quote.
- [ ] Tambah `Okx` ke `ExchangeKind` enum + loader + MultiExchangeClient dispatch.
- [ ] Test: akun OKX dry-run; verify balance, market data, OHLCV, order placement (testnet).

### Fase 3 — Aggregator & polish
- [ ] `AggregatorView` rollup: total BTC, PnL, win rate per akun.
- [ ] `GET /btc/aggregate?format=json|prom`.
- [ ] `/btc_aggregate` Telegram command + laporan periodik (opt-in).
- [ ] Account-level enable/disable endpoint + command.
- [ ] `metrics.json` rolling counters.

### Fase 4 — Hardening
- [ ] Secrets handling: file/env based, masker, no echo.
- [ ] Healthcheck per akun.
- [ ] Span tracing `account_id`/`exchange` end-to-end.
- [ ] Doc update: `SKILL.md`, `README`/`docker-compose` env, `.env.example`.
- [ ] Load test: 3 akun (2 Binance + 1 OKX) paralel, scanner interval tight, monitor posisi di semua.
- [ ] Failure test: matikan satu API key exchange → akun lain tetap jalan; supervisor restart akun yang error.

---

## 10. Risiko & mitigasi

| Risiko | Dampak | Mitigasi |
|---|---|---|
| Race antar akun tulis ke file shared (skeleton lama) | State corruption | Refactor `MemoryStore` per-akun path di Fase 1; tidak ada path shared |
| Rate-limit OKX beda drastis dari Binance | Order gagal diam-diam | `TokenBucket` + log warn + circuit-breaker sederhana (skip pair N menit setelah 429) |
| `?account=ID` typo → request ke akun salah | Salah close posisi | Validasi ID; fallback ke `default`; Telegram konfirmasi untuk `/btc_close`/`/btc_sell` lintas akun |
| Legacy user upgrade tanpa set BTC_ACCOUNTS_JSON | Bingung kenapa hanya 1 akun | Fallback otomatis ke akun `default`; log info "single-account legacy mode" |
| JSON config dengan secret bocor ke log | Credential leak | `Credentials::EnvKeySecret` env-only; serialization `Credentials` di-skip lewat `#[serde(skip)]` di struct config eksternal |
| OKX pair suffix `-BTC` vs Binance `BTC` quote | Salah pair | Adapter normalisasi ke internal; log pair name as-used saat call |
| Crash loop akun (API key invalid) | Spam log | Supervisor pakai exponential backoff cap 5 menit; alert via Telegram setelah 3x restart |
| `MemoryStore::init_defaults` clobber legacy files | Kehilangan posisi | Mode lama tulis di path lama; `init_defaults` skip kalau file ada (`if !path.exists()` sudah benar) |

---

## 11. Test plan

- **Unit:**
  - `with_retry` extracted ke `util::with_retry`, test transient vs permanent.
  - `OkxClient::sign` golden test vectors (timestamp, path, body, expected base64 sig).
  - `Credentials` env loader — env ada / tidak ada / malformed JSON.
  - `AccountSpec` validator — id duplikat, exchange tak dikenal, missing credential.
  - Pair conversion `SOLBTC` ↔ `SOL-BTC` ↔ `SOLUSDT` ↔ `SOL-USDT`.
  - `AggregatorView` rollup math (multi-akun PnL, win rate).
- **Integration (dengan testnet):**
  - Binance testnet + OKX demo: end-to-end `/btc_buy` → monitor → close; verify JSON state di path per-akun.
  - Dry-run 2 akun paralel, verify tidak ada race di filesystem.
- **Manual smoke:**
  - 1 akun Binance (legacy) — harus identik dengan main.
  - 2 akun Binance (1 mainnet, 1 testnet) — parallel dry-run.
  - 1 akun Binance + 1 akun OKX — parallel dry-run.
  - Disable 1 akun via Telegram, verify scanner berhenti, akun lain jalan.
  - Kill API key, verify supervisor restart dgn backoff.

---

## 12. Ringkasan urutan eksekusi singkat

```
Fase 0 ─► trait + registry + dispatcher (1 PR, zero behavior change)
   │
Fase 1 ─► per-account runtime + state isolation + multi-switch Telegram (1-2 PR)
   │
Fase 2 ─► OKX adapter + integration (1 PR)
   │
Fase 3 ─► aggregator + enable/disable + metrics (1 PR)
   │
Fase 4 ─► hardening, docs, load test (1 PR)
```

Estimasi: 5 PR inkremental, masing-masing bisa di-review & di-merge independen. Setiap fase menambah capability tanpa menyentuh fase sebelumnya.

---

## Lampiran A: Diff ringkas Fase 0 (konseptual)

```
+ src/multi_exchange.rs            # MultiExchangeClient router
+ src/account_spec.rs              # AccountSpec, ExchangeKind, RiskOverrides, Credentials
+ src/util/retry.rs                # extracted with_retry
~ src/config.rs                    # tambah loader BTC_ACCOUNTS_JSON / LIST / legacy fallback
~ src/main.rs                      # build dispatcher + spawn per akun
~ src/server.rs                    # ?account=ID plumbing
~ src/telegram_bot.rs              # active_account per chat
~ src/scanner.rs                   # signature: exchange: Arc<MultiExchangeClient> (tapi .default() OK)
~ src/position_monitor.rs          # sama
~ src/execution_engine.rs          # sama
```

Tambahan di Fase 2:
```
+ src/okx.rs
+ src/util/rate_limit.rs
~ src/exchange.rs                  # tambah impl ExchangeClient for OkxClient
```

Tidak ada perubahan ke `engines/`, `indicators.rs`, `llm.rs`, `format.rs`, `sanitize.rs`, `models.rs`.
