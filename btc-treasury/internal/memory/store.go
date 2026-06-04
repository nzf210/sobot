package memory

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"btc-treasury/internal/config"
	"btc-treasury/internal/models"
)

type MemoryStore struct {
	dataDir    string
	accountDir string
	accountID  string
	exchange   config.ExchangeKind
	lock       sync.RWMutex
}

func NewMemoryStore(dataDir string) *MemoryStore {
	return NewMemoryStoreWithAccount(dataDir, "", "")
}

func NewMemoryStoreWithAccount(dataDir string, accountID string, exchange config.ExchangeKind) *MemoryStore {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		absDataDir = dataDir
	}
	_ = os.MkdirAll(absDataDir, 0755)

	isLegacyDefault := accountID == "" || accountID == "default"
	accountDir := absDataDir

	if !isLegacyDefault {
		if exchange != "" {
			accountDir = filepath.Join(absDataDir, "accounts", accountID, string(exchange))
		} else {
			accountDir = filepath.Join(absDataDir, "accounts", accountID)
		}
		_ = os.MkdirAll(accountDir, 0755)
	}

	store := &MemoryStore{
		dataDir:    absDataDir,
		accountDir: accountDir,
		accountID:  accountID,
		exchange:   exchange,
	}
	store.initDefaults()
	return store
}

func (s *MemoryStore) AccountID() string {
	return s.accountID
}

func (s *MemoryStore) Exchange() config.ExchangeKind {
	return s.exchange
}

func (s *MemoryStore) initDefaults() {
	defaults := []struct {
		filename string
		content  string
	}{
		{"btc-treasury.json", `{"current_btc":0,"previous_btc":0,"btc_growth_7d":0,"btc_growth_30d":0,"stable_value":0,"usdt_balance":0,"last_update":"","btc_treasury_vault":0,"compound_balance":0,"total_trades":0,"winning_trades":0,"losing_trades":0,"trading_paused_until":"","consecutive_losses":0}`},
		{"btc-decision-log.json", `[]`},
		{"btc-config.json", `{"enabled":true,"llm_activation_threshold":0.85,"min_confidence":0.80,"max_exposure":0.50,"daily_loss_limit_btc":0.0005,"max_consecutive_losses":3,"safe_mode_volatility":9.0,"safe_mode_drawdown":0.05,"scanner_pairs":["BTCUSDT","SOLBTC","ETHBTC","BNBBTC","XRPBTC","ADABTC","LINKBTC","SUIBTC","AVAXBTC","DOGEBTC"],"take_profit_pct":5.5,"stop_loss_pct":-1.5,"trailing_tp_pct":3.0,"use_trailing":true,"max_positions":1,"risk_per_trade_pct":0.01,"initial_capital_usdt":50.0,"min_score_threshold":80.0,"compound_pct":0.50,"treasury_pct":0.50,"dry_run":true}`},
		{"btc-positions.json", `[]`},
		{"btc-lessons.json", `[]`},
	}

	// SKILL.md setup in dataDir
	skillPath := filepath.Join(s.dataDir, "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		var skillContent string
		// Try to read existing SKILL.md from typical relative paths
		candidates := []string{"SKILL.md", "../SKILL.md", "/app/SKILL.md"}
		for _, p := range candidates {
			if data, err := os.ReadFile(p); err == nil {
				skillContent = string(data)
				break
			}
		}
		if skillContent == "" {
			skillContent = "# BTC Treasury Advisor (Spot)\n- Autonomous Binance spot scanner\n- Market regime detection\n- Risk assessment\n- LLM reasoning"
		}
		_ = os.WriteFile(skillPath, []byte(skillContent), 0644)
	}

	for _, d := range defaults {
		path := filepath.Join(s.accountDir, d.filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = os.WriteFile(path, []byte(d.content), 0644)
		}
	}
}

func (s *MemoryStore) readJSON(filename string, target interface{}) error {
	s.lock.RLock()
	defer s.lock.RUnlock()

	path := filepath.Join(s.accountDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *MemoryStore) writeJSON(filename string, data interface{}) {
	s.lock.Lock()
	defer s.lock.Unlock()

	path := filepath.Join(s.accountDir, filename)
	tmpPath := path + ".tmp"

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("memory: failed to serialize data: %v", err)
		return
	}

	if err := os.WriteFile(tmpPath, bytes, 0644); err != nil {
		log.Printf("memory: failed to write tmp %s: %v", tmpPath, err)
		return
	}

	// Open temp file and sync to disk
	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
	if err == nil {
		_ = f.Sync()
		f.Close()
	}

	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("memory: failed to rename %s -> %s: %v", tmpPath, path, err)
		_ = os.Remove(tmpPath)
	}
}

func (s *MemoryStore) GetTreasuryState() models.BtcTreasuryState {
	var state models.BtcTreasuryState
	_ = s.readJSON("btc-treasury.json", &state)
	return state
}

func (s *MemoryStore) SaveTreasuryState(state models.BtcTreasuryState) {
	state.LastUpdate = time.Now().UTC().Format(time.RFC3339)
	s.writeJSON("btc-treasury.json", &state)
}

func (s *MemoryStore) SyncInitialBalances(liveBtc, liveUsdt float64) {
	state := s.GetTreasuryState()
	state.CurrentBtc = liveBtc
	state.PreviousBtc = liveBtc
	state.UsdtBalance = liveUsdt
	state.StableValue = liveUsdt
	s.SaveTreasuryState(state)
	log.Printf("Synced treasury with Binance balances: BTC=%.8f USDT=%.2f", liveBtc, liveUsdt)
}

func (s *MemoryStore) UpdateGrowthRatios() {
	state := s.GetTreasuryState()
	prev := state.PreviousBtc
	if prev > 0.0 {
		ratio := (state.CurrentBtc - prev) / prev
		state.BtcGrowth7d = ratio
		state.BtcGrowth30d = ratio
	}
	s.SaveTreasuryState(state)
}

func (s *MemoryStore) ResyncAfterFill(liveBtc, liveUsdt float64) {
	state := s.GetTreasuryState()
	if state.PreviousBtc <= 0.0 {
		state.PreviousBtc = state.CurrentBtc
	}
	state.CurrentBtc = liveBtc
	state.UsdtBalance = liveUsdt
	state.StableValue = liveUsdt
	s.SaveTreasuryState(state)
	s.UpdateGrowthRatios()
	log.Printf("Treasury re-synced after fill: BTC=%.8f USDT=%.2f", liveBtc, liveUsdt)
}

func (s *MemoryStore) DeductBalanceForBuy(pair string, quoteSpent float64) {
	if quoteSpent <= 0.0 {
		return
	}
	p := strings.ToUpper(pair)
	state := s.GetTreasuryState()

	if strings.HasSuffix(p, "BTC") && p != "BTCUSDT" {
		state.CurrentBtc = math.Max(state.CurrentBtc-quoteSpent, 0.0)
		log.Printf("Treasury: deducted %.8f BTC for %s buy → current_btc=%.8f", quoteSpent, pair, state.CurrentBtc)
	} else {
		state.UsdtBalance = math.Max(state.UsdtBalance-quoteSpent, 0.0)
		state.StableValue = state.UsdtBalance
		log.Printf("Treasury: deducted %.2f USDT for %s buy → usdt_balance=%.2f", quoteSpent, pair, state.UsdtBalance)
	}
	s.SaveTreasuryState(state)
}

func (s *MemoryStore) LogDecision(record models.BtcDecisionRecord) {
	s.lock.Lock()
	defer s.lock.Unlock()

	path := filepath.Join(s.accountDir, "btc-decision-log.json")
	tmpPath := path + ".tmp"

	var records []models.BtcDecisionRecord
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &records)
	}
	records = append(records, record)

	bytes, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		log.Printf("memory: failed to serialize decision log: %v", err)
		return
	}

	if err := os.WriteFile(tmpPath, bytes, 0644); err != nil {
		log.Printf("memory: failed to write decision log tmp: %v", err)
		return
	}

	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
	if err == nil {
		_ = f.Sync()
		f.Close()
	}

	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("memory: failed to rename decision log tmp: %v", err)
		_ = os.Remove(tmpPath)
	}
}

func (s *MemoryStore) GetDecisions() []models.BtcDecisionRecord {
	var records []models.BtcDecisionRecord
	_ = s.readJSON("btc-decision-log.json", &records)
	return records
}

func (s *MemoryStore) GetConfig() models.BtcConfig {
	var cfg models.BtcConfig
	_ = s.readJSON("btc-config.json", &cfg)
	return cfg
}

func (s *MemoryStore) SaveConfig(config models.BtcConfig) {
	s.writeJSON("btc-config.json", &config)
}

func (s *MemoryStore) GetPositions() []models.BtcAdvisoryPosition {
	var positions []models.BtcAdvisoryPosition
	_ = s.readJSON("btc-positions.json", &positions)
	return positions
}

func (s *MemoryStore) SavePositions(positions []models.BtcAdvisoryPosition) {
	s.writeJSON("btc-positions.json", &positions)
}

func (s *MemoryStore) GetLessons() []string {
	var lessons []string
	_ = s.readJSON("btc-lessons.json", &lessons)
	return lessons
}

func (s *MemoryStore) AddLesson(lesson string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	path := filepath.Join(s.accountDir, "btc-lessons.json")
	tmpPath := path + ".tmp"

	var lessons []string
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &lessons)
	}
	lessons = append(lessons, lesson)

	bytes, err := json.MarshalIndent(lessons, "", "  ")
	if err != nil {
		log.Printf("memory: failed to serialize lessons: %v", err)
		return
	}

	if err := os.WriteFile(tmpPath, bytes, 0644); err != nil {
		log.Printf("memory: failed to write lessons tmp: %v", err)
		return
	}

	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
	if err == nil {
		_ = f.Sync()
		f.Close()
	}

	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("memory: failed to rename lessons tmp: %v", err)
		_ = os.Remove(tmpPath)
	}
}

func (s *MemoryStore) UpdateTreasuryOnClose(pair string, pnlPct, positionSizeQuote, btcPrice float64) bool {
	cfg := s.GetConfig()
	state := s.GetTreasuryState()
	pnlMultiplier := 1.0 + (pnlPct / 100.0)
	grossPnl := positionSizeQuote * (pnlMultiplier - 1.0)

	exitValue := positionSizeQuote * pnlMultiplier
	roundTripFee := (positionSizeQuote + exitValue) * cfg.TakerFeePct
	netPnl := grossPnl - roundTripFee

	isBtcQuote := math.Abs(btcPrice-1.0) < 1e-9

	if !isBtcQuote && btcPrice <= 0.0 {
		log.Printf("ERROR: Refusing to close %s — btc_price must be > 0 for USDT-quote pair (got %f). Fetch live BTCUSDT price before retrying.", pair, btcPrice)
		return false
	}

	price := btcPrice
	if isBtcQuote {
		price = 1.0
	}

	var btcDelta float64
	if isBtcQuote {
		btcDelta = netPnl
	} else {
		btcDelta = netPnl / price
	}

	if pnlPct > 0.0 {
		vaultBtc := btcDelta * cfg.TreasuryPct
		compoundBtc := btcDelta * cfg.CompoundPct
		state.PreviousBtc = state.CurrentBtc
		state.CurrentBtc += btcDelta
		state.BtcTreasuryVault += vaultBtc
		state.CompoundBalance += compoundBtc
		state.TotalTrades++
		state.WinningTrades++

		unit := "USDT"
		if isBtcQuote {
			unit = "BTC"
		}
		log.Printf("Position %s closed at +%.2f%%. BTC treasury grew by %.8f BTC (profit: %.2f %s, fee: %.2f %s). Split: %.8f vault + %.8f compound",
			pair, pnlPct, btcDelta, grossPnl, unit, roundTripFee, unit, vaultBtc, compoundBtc)
	} else {
		state.PreviousBtc = state.CurrentBtc
		state.CurrentBtc = math.Max(state.CurrentBtc+btcDelta, 0.0)
		state.TotalTrades++
		state.LosingTrades++

		unit := "USDT"
		if isBtcQuote {
			unit = "BTC"
		}
		log.Printf("Position %s closed at %.2f%%. BTC treasury reduced by %.8f BTC (loss: %.2f %s, fee: %.2f %s)",
			pair, pnlPct, math.Abs(btcDelta), math.Abs(grossPnl), unit, roundTripFee, unit)
	}

	s.SaveTreasuryState(state)
	s.UpdateGrowthRatios()
	return true
}

func (s *MemoryStore) LoadSkills() string {
	path := filepath.Join(s.dataDir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *MemoryStore) LoadLessonsContext() string {
	lessons := s.GetLessons()
	if len(lessons) == 0 {
		return ""
	}

	// Take only 3 most recent, each truncated to 250 chars
	start := len(lessons) - 3
	if start < 0 {
		start = 0
	}
	recent := lessons[start:]

	out := "\n\nRECENT LESSONS (3 most recent):\n"
	// Output in reverse order (freshest first)
	for i := len(recent) - 1; i >= 0; i-- {
		l := recent[i]
		truncated := l
		if len(l) > 250 {
			truncated = l[:247] + "..."
		}
		out += fmt.Sprintf("%d. %s\n", len(recent)-i, truncated)
	}
	return out
}
