# Solana Hybrid DLMM + Sniper + LLM System

Production-oriented hybrid architecture:

- Go orchestration layer
- TypeScript Solana execution layer
- Risk engine
- Rule engine
- Historical memory
- LLM reasoning pipeline
- DLMM automation
- Momentum sniper
- Structured telemetry

## Stack

### Backend
- Go
- Gin
- SQLite
- Zap Logger

### Executor
- TypeScript
- Solana Web3.js
- Jupiter SDK
- Meteora SDK (stub)
- Anchor

## Configuration

Before running the application, you need to set up your environment variables. A sample file is provided.

```bash
cp .env.sample .env
```

Update the `.env` file with your Solana RPC URL, wallet password, LLM API keys, and database paths.

### Generating a Secure Wallet

Instead of storing raw private keys in `.env`, we use an encrypted wallet file (`wallet.enc`).

To generate a new wallet or encrypt an existing private key:
```bash
cd executor-ts
npm run generate-wallet
```
This script will prompt you for a base58 private key (or generate a new one if left blank) and a password to encrypt it. Ensure the `WALLET_PASSWORD` in your `.env` matches the password you provide here.

Additional business logic configurations (like liquidity and positions limits) can be found in `configs/default.json`.

## Run

### Go Backend

```bash
cd backend-go
go mod tidy
go run ./cmd/main.go
```

### TS Executor

```bash
cd executor-ts
npm install
npm run dev
```