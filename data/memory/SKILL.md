# Hybrid Solana Sniper — Agent Skills & Capabilities

Data dir: `data/memory/`

## Core Skills

### 1. Autonomous Token Sniffer (Auto-Scanner)
The bot constantly patrols the blockchain for newly created liquidity pools via DexScreener.
- **Behavior:** Polls global new pairs every 10 seconds, filters for Solana, and extracts the newest tokens.
- **Action:** Triggers an automatic deep-dive analysis on any unseen token.

### 2. Manual Analysis (`/analyze <address>`)
Allows manual intervention via Telegram.
- **Input:** Token Mint Address.
- **Output:** Immediate fetching of Liquidity, Volume, Market Cap, Buy/Sell Ratio, and Wash Trading Probability, followed by an AI decision and potential swap execution.

### 3. Risk Engine (The Shield)
Acts as the first line of defense before consulting the AI.
- **Rules:** Rejects tokens immediately if liquidity is under $10,000 to avoid honeypots and dead pools.
- **Wash Trading Check:** Applies heuristic rules against pairs with suspiciously high volume but zero organic trading behavior.

### 4. LLM AI Reasoning Engine
The "Brain" of the sniper. Consumes financial metrics and contextual memory.
- **Input:** Financial numbers + `strategies.json` + `lessons.json` + `signal-weights.json`.
- **Output:** Returns a strictly formatted JSON decision (`BUY`, `SELL`, `HOLD`, `MICRO_ENTRY_ONLY`) with a confidence score and narrative evaluation.

### 5. Jupiter V6 Executor (The Sniper)
The TypeScript execution arm that handles raw blockchain interactions.
- **Behavior:** Once a token is `APPROVED` by the LLM, the orchestrator triggers a background webhook to the Executor.
- **Action:** Decrypts `wallet.enc`, computes the best routing via Jupiter V6, sets slippage, and blasts the transaction to the Solana mainnet.

## Memory Management (Continuous Learning)

### 1. `decision-log.json`
Every AI reasoning sequence and subsequent decision is persistently logged here. Used for post-mortem analysis of win/loss rates.

### 2. `lessons.json`
Stores manual or self-reflected lessons (e.g., "Tokens with CTO narratives fail 80% of the time"). The AI injects this directly into its prompt to avoid repeating past mistakes.

### 3. `strategies.json`
Contains textual definitions of the bot's current tactical approaches (e.g., "Snipe Low Cap", "Meme Coin Trend Riding"). The bot adopts the persona defined here.

### 4. `signal-weights.json`
Mathematical weights mapping how much importance the bot should give to Liquidity versus Volume versus Organic Score.

### 5. `config.json` & `user-config.json`
Stores the runtime switches, such as `auto_trade`, `risk_tolerance`, and global on/off switches.

## Future / Planned Capabilities

### Auto-Sell & Position Management (Take Profit / Stop Loss)
- *In Development:* A position tracker that monitors tokens bought by the Executor and automatically triggers a sell when reaching a +20% Take Profit or -10% Stop Loss.