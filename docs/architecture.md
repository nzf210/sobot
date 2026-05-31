# Architecture Overview

## System Components

```
┌─────────────────────────────────────────────────────────────────┐
│                     Solana Hybrid System                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐   │
│  │  backend-go  │  │ executor-ts  │  │    btc-treasury      │   │
│  │ (port 8089)│  │  (port 3009) │  │     (port 8090)      │   │
│  └──────┬───────┘  └──────┬───────┘  └──────────┬───────────┘   │
│ │                 │                      │               │
│         │    HTTP/API │                      │               │
│         │                 │                      │               │
│         └────────┬────────┘                      │               │
│                  │                               │               │
│         ┌────────▼────────┐                      │               │
│         │ 14-Stage Pipeline│ │               │
│         │  Orchestrator   │                       │               │
│         └────────┬────────┘                       │               │
│                  │                               │               │
│    ┌─────────────┼─────────────┐                  │               │
│    ▼             ▼             ▼                  ▼               │
│ ┌──────┐  ┌──────────┐  ┌─────────┐  ┌──────────────────┐     │
│ │Risk │  │Engines   │  │ Position│ │  BTC Advisory │     │
│ │Engine│  │(14 types)│  │ Manager │  │  Engine          │     │
│ └──────┘  └──────────┘  └─────────┘  └──────────────────┘     │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                   Telegram Bots │   │
│  │  • Solana Bot (token analysis, positions, config)        │   │
│  │  • BTC Treasury Bot (advisory, treasury status)          │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Backend Go (Port 8089)

**Responsibilities:**
- 14-stage pipeline orchestration
- Token scanning (PumpFun, Raydium, Meteora)
- Position management (TP/SL/trailing)
- Telegram bot interface
- Risk engine coordination

**Key Components:**
- `PipelineOrchestrator` — 14-stage analysis pipeline
- `Scanner` — Multi-DEX token discovery
- `PositionManager` — TP/SL monitoring, trailing stops
- `RuleEngine` — Pre-trade validation
- `TelegramBot` — User commands

---

## Executor TypeScript (Port 3009)

**Responsibilities:**
- Jupiter Swap v6 execution
- Wallet management (encrypted)
- Transaction signing & submission

**Key Components:**
- `WalletLoader` — AES-256-GCM encrypted keypair
- `JupiterExecutor` — Quote → Transaction → Sign → Submit
- `DLMMStub` — Meteora DLMM placeholder

---

## BTC Treasury Rust (Port 8090)

**Responsibilities:**
- HyperLiquid market monitoring
- BTC treasury advisory
- Quant + LLM hybrid analysis

**Key Components:**
- `AdvisoryEngine` — Market regime classification
- `Scanner` — Binance pair monitoring
- `TelegramBot` — Treasury bot interface

---

## 14-Stage Pipeline

| Stage | Name | Purpose |
|-------|------|---------|
| 1 | Metrics Normalization | Clamp negatives, normalize ratios |
| 2 | Metric-only Rule Check | Fast rejection on basic metrics |
| 3 | Deployer Reputation | DexScreener deployer history |
| 4 | Wallet Cluster Detection | Bot/shill activity detection |
| 5 | Holder Distribution | Solscan top-10 holders |
| 6 | Liquidity Stability | Sliding window rug detection |
| 7 | Jupiter Intelligence | Price impact at order size |
| 8 | Momentum Engine | Volume/price momentum scoring |
| 9 | Market Regime Detector | SOL trend classification |
| 10 | Confidence Engine | Weighted factor combination |
| 11 | Engine-dependent Rules | Multi-engine validation gates |
| 12 | Dynamic Position Sizing | Confidence → size mapping |
| 13 | Portfolio Risk Engine | Position/drawdown limits |
| 14 | LLM Narrative Analysis | Final decision (BUY/SELL/HOLD) |

---

## Data Flow

```
Token Address
     │
     ▼
┌─────────────┐
│  Metrics    │ ← DexScreener API
│  Fetcher    │
└──────┬──────┘
     │
     ▼
┌─────────────┐
│ Rule Engine │ ← Fast rejection (liquidity, volume, mcap)
│ (Stage 2)   │
└──────┬──────┘
     │
     ▼
┌─────────────┐
│ 14 Engines  │ ← Parallel analysis
│              │   (Deployer, Holders, Liquidity, Momentum, etc.)
└──────┬──────┘
     │
     ▼
┌─────────────┐
│ Confidence  │ ← Weighted scoring
│   Engine    │
└──────┬──────┘
     │
     ▼
┌─────────────┐
│   LLM       │ ← Final decision
│  Narrative  │
└──────┬──────┘
     │
     ▼
┌─────────────┐
│  Executor   │ ← Jupiter Swap v6
│  (if BUY)   │
└──────┬──────┘
     │
     ▼
┌─────────────┐
│  Position   │ ← TP/SL monitoring
│  Manager    │
└─────────────┘
```

---

## Security Architecture

```
┌─────────────────────────────────────────────────────┐
│                   Security Layers                   │
├─────────────────────────────────────────────────────┤
│                                                     │
│  1. Wallet Encryption                               │
│     AES-256-GCM + scrypt KDF                       │
│     Password never stored                          │
│                                                     │
│  2. API Authentication                             │
│     X-API-Key header between services              │
│     Strong random keys (openssl rand -hex 32)      │
│                                                     │
│  3. Telegram Whitelist                             │
│     Chat ID-based access control                   │
│     Only whitelisted users can command             │
│                                                     │
│  4. Dry-Run Mode                                   │
│     Default: ON                                    │
│     No real trades until explicitly disabled      │
│                                                     │
│  5. Auto-Trade Control                             │
│     Default: OFF                                  │
│     Manual approval required for execution        │
│                                                     │
└─────────────────────────────────────────────────────┘
```

---

## Telegram Bot Integration

### Solana Bot Commands
```
/help          — Show all commands
/analyze <addr>— Analyze token
/status        — System health
/positions     — Open positions
/close <idx>  — Close position
/closeall     — Close all
/dryrun on|off— Toggle dry-run
/config       — Show config
/setconfig    — Update config
```

### BTC Treasury Bot Commands
```
/status    — Treasury status
/advisory — Get advisory
/positions— Open positions
/config   — Show config
/report   — Force report
```

---

## Environment Variables

See [PARAMETERS.md](PARAMETERS.md) for complete reference.

---

## Docker Network

All services communicate via internal Docker bridge network `solana-net`:

```
backend    → executor  (http://executor:3009)
backend    → btc-treasury (http://btc-treasury:8090)
```

External access via exposed ports:
- `8089` → backend-go
- `3009` → executor-ts
- `8090` → btc-treasury