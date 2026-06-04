package memory

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"btc-treasury/internal/config"
	"btc-treasury/internal/models"
)

// GormDBStore implements Store interface using GORM
type GormDBStore struct {
	db        *gorm.DB
	accountID string
	exchange  config.ExchangeKind
}

func NewGormDBStore(driver, dsn, accountID string, exchange config.ExchangeKind) (Store, error) {
	var dialector gorm.Dialector
	if strings.ToLower(driver) == "postgres" || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dialector = postgres.Open(dsn)
	} else {
		// Default to SQLite
		dialector = sqlite.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("gorm: failed to connect to database: %w", err)
	}

	// Auto-migrate tables using shared models from internal/models
	err = db.AutoMigrate(
		&models.DbAccountSpec{},
		&models.DbAccountExchange{},
		&models.DbAccountConfig{},
		&models.DbTreasuryState{},
		&models.DbOpenPosition{},
		&models.DbDecisionLog{},
		&models.DbTradingLesson{},
		&models.DbSystemSkill{},
	)
	if err != nil {
		return nil, fmt.Errorf("gorm: auto migrate failed: %w", err)
	}

	store := &GormDBStore{
		db:        db,
		accountID: accountID,
		exchange:  exchange,
	}

	return store, nil
}

func (s *GormDBStore) AccountID() string {
	return s.accountID
}

func (s *GormDBStore) Exchange() config.ExchangeKind {
	return s.exchange
}

func (s *GormDBStore) GetTreasuryState() models.BtcTreasuryState {
	var dbState models.DbTreasuryState
	err := s.db.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).First(&dbState).Error
	if err != nil {
		// Return default empty state
		return models.BtcTreasuryState{
			CurrentBtc:         0.0,
			PreviousBtc:        0.0,
			BtcGrowth7d:        0.0,
			BtcGrowth30d:       0.0,
			StableValue:        0.0,
			UsdtBalance:        0.0,
			LastUpdate:         "",
			BtcTreasuryVault:   0.0,
			CompoundBalance:    0.0,
			TotalTrades:        0,
			WinningTrades:      0,
			LosingTrades:       0,
			TradingPausedUntil: "",
			ConsecutiveLosses:  0,
		}
	}

	return models.BtcTreasuryState{
		CurrentBtc:         dbState.CurrentBtc,
		PreviousBtc:        dbState.PreviousBtc,
		BtcGrowth7d:        dbState.BtcGrowth7d,
		BtcGrowth30d:       dbState.BtcGrowth30d,
		StableValue:        dbState.StableValue,
		UsdtBalance:        dbState.UsdtBalance,
		LastUpdate:         dbState.LastUpdate,
		BtcTreasuryVault:   dbState.BtcTreasuryVault,
		CompoundBalance:    dbState.CompoundBalance,
		TotalTrades:        dbState.TotalTrades,
		WinningTrades:      dbState.WinningTrades,
		LosingTrades:       dbState.LosingTrades,
		TradingPausedUntil: dbState.TradingPausedUntil,
		ConsecutiveLosses:  dbState.ConsecutiveLosses,
	}
}

func (s *GormDBStore) SaveTreasuryState(state models.BtcTreasuryState) {
	dbState := models.DbTreasuryState{
		AccountID:          s.accountID,
		ExchangeKind:       string(s.exchange),
		CurrentBtc:         state.CurrentBtc,
		PreviousBtc:        state.PreviousBtc,
		BtcGrowth7d:        state.BtcGrowth7d,
		BtcGrowth30d:       state.BtcGrowth30d,
		StableValue:        state.StableValue,
		UsdtBalance:        state.UsdtBalance,
		LastUpdate:         state.LastUpdate,
		BtcTreasuryVault:   state.BtcTreasuryVault,
		CompoundBalance:    state.CompoundBalance,
		TotalTrades:        state.TotalTrades,
		WinningTrades:      state.WinningTrades,
		LosingTrades:       state.LosingTrades,
		TradingPausedUntil: state.TradingPausedUntil,
		ConsecutiveLosses:  state.ConsecutiveLosses,
		UpdatedAt:          time.Now(),
	}

	if err := s.db.Save(&dbState).Error; err != nil {
		log.Printf("gorm: failed to save treasury state: %v", err)
	}
}

func (s *GormDBStore) SyncInitialBalances(liveBtc, liveUsdt float64) {
	state := s.GetTreasuryState()
	state.CurrentBtc = liveBtc
	state.PreviousBtc = liveBtc
	state.UsdtBalance = liveUsdt
	state.StableValue = liveUsdt
	s.SaveTreasuryState(state)
	log.Printf("Synced treasury with balances in DB: BTC=%.8f USDT=%.2f", liveBtc, liveUsdt)
}

func (s *GormDBStore) UpdateGrowthRatios() {
	state := s.GetTreasuryState()
	prev := state.PreviousBtc
	if prev > 0.0 {
		ratio := (state.CurrentBtc - prev) / prev
		state.BtcGrowth7d = ratio
		state.BtcGrowth30d = ratio
	}
	s.SaveTreasuryState(state)
}

func (s *GormDBStore) ResyncAfterFill(liveBtc, liveUsdt float64) {
	state := s.GetTreasuryState()
	if state.PreviousBtc <= 0.0 {
		state.PreviousBtc = state.CurrentBtc
	}
	state.CurrentBtc = liveBtc
	state.UsdtBalance = liveUsdt
	state.StableValue = liveUsdt
	s.SaveTreasuryState(state)
	s.UpdateGrowthRatios()
	log.Printf("Treasury re-synced after fill in DB: BTC=%.8f USDT=%.2f", liveBtc, liveUsdt)
}

func (s *GormDBStore) DeductBalanceForBuy(pair string, quoteSpent float64) {
	if quoteSpent <= 0.0 {
		return
	}
	p := strings.ToUpper(pair)
	state := s.GetTreasuryState()

	if strings.HasSuffix(p, "BTC") && p != "BTCUSDT" {
		state.CurrentBtc = math.Max(state.CurrentBtc-quoteSpent, 0.0)
		log.Printf("Treasury DB: deducted %.8f BTC for %s buy → current_btc=%.8f", quoteSpent, pair, state.CurrentBtc)
	} else {
		state.UsdtBalance = math.Max(state.UsdtBalance-quoteSpent, 0.0)
		state.StableValue = state.UsdtBalance
		log.Printf("Treasury DB: deducted %.2f USDT for %s buy → usdt_balance=%.2f", quoteSpent, pair, state.UsdtBalance)
	}
	s.SaveTreasuryState(state)
}

func (s *GormDBStore) LogDecision(record models.BtcDecisionRecord) {
	rawBytes, err := json.Marshal(record)
	if err != nil {
		log.Printf("gorm: failed to marshal decision: %v", err)
		return
	}

	dbRecord := models.DbDecisionLog{
		AccountID:    s.accountID,
		ExchangeKind: string(s.exchange),
		Timestamp:    record.Timestamp,
		Pair:         record.MarketData.Pair,
		MarketRegime: record.MarketData.MarketRegime,
		Confidence:   record.Advisory.Confidence,
		ActionTaken:  record.ActionTaken,
		RawRecord:    string(rawBytes),
		CreatedAt:    time.Now(),
	}

	if err := s.db.Create(&dbRecord).Error; err != nil {
		log.Printf("gorm: failed to create decision log: %v", err)
	}
}

func (s *GormDBStore) GetDecisions() []models.BtcDecisionRecord {
	var dbRecords []models.DbDecisionLog
	err := s.db.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).Order("id asc").Find(&dbRecords).Error
	if err != nil {
		log.Printf("gorm: failed to get decisions: %v", err)
		return nil
	}

	records := make([]models.BtcDecisionRecord, len(dbRecords))
	for i, r := range dbRecords {
		_ = json.Unmarshal([]byte(r.RawRecord), &records[i])
	}
	return records
}

func (s *GormDBStore) defaultBtcConfig() models.BtcConfig {
	return models.BtcConfig{
		Enabled:                true,
		LlmActivationThreshold: 0.85,
		MinConfidence:          0.80,
		MaxExposure:            0.50,
		DailyLossLimitBtc:      0.0005,
		MaxConsecutiveLosses:   3,
		SafeModeVolatility:     9.0,
		SafeModeDrawdown:       0.05,
		ScannerPairs:           []string{"BTCUSDT", "SOLBTC", "ETHBTC", "BNBBTC", "XRPBTC", "ADABTC", "LINKBTC", "SUIBTC", "AVAXBTC", "DOGEBTC"},
		TakeProfitPct:          5.5,
		StopLossPct:            -1.5,
		TrailingTpPct:          3.0,
		UseTrailing:            true,
		MaxPositions:           1,
		RiskPerTradePct:        0.01,
		InitialCapitalUsdt:     50.0,
		MinScoreThreshold:      80.0,
		CompoundPct:            0.50,
		TreasuryPct:            0.50,
		DryRun:                 true,
		TakerFeePct:            0.001,
	}
}

func (s *GormDBStore) GetConfig() models.BtcConfig {
	var dbCfg models.DbAccountConfig
	err := s.db.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).First(&dbCfg).Error
	if err != nil {
		// Populate and save default config
		cfg := s.defaultBtcConfig()
		s.SaveConfig(cfg)
		return cfg
	}

	var pairs []string
	if dbCfg.ScannerPairs != "" {
		for _, p := range strings.Split(dbCfg.ScannerPairs, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				pairs = append(pairs, p)
			}
		}
	}

	return models.BtcConfig{
		Enabled:                dbCfg.Enabled,
		LlmActivationThreshold: dbCfg.LlmActivationThreshold,
		MinConfidence:          dbCfg.MinConfidence,
		MaxExposure:            dbCfg.MaxExposure,
		DailyLossLimitBtc:      dbCfg.DailyLossLimitBtc,
		MaxConsecutiveLosses:   dbCfg.MaxConsecutiveLosses,
		SafeModeVolatility:     dbCfg.SafeModeVolatility,
		SafeModeDrawdown:       dbCfg.SafeModeDrawdown,
		ScannerPairs:           pairs,
		TakeProfitPct:          dbCfg.TakeProfitPct,
		StopLossPct:            dbCfg.StopLossPct,
		TrailingTpPct:          dbCfg.TrailingTpPct,
		UseTrailing:            dbCfg.UseTrailing,
		MaxPositions:           dbCfg.MaxPositions,
		RiskPerTradePct:        dbCfg.RiskPerTradePct,
		InitialCapitalUsdt:     dbCfg.InitialCapitalUsdt,
		MinScoreThreshold:      dbCfg.MinScoreThreshold,
		CompoundPct:            dbCfg.CompoundPct,
		TreasuryPct:            dbCfg.TreasuryPct,
		DryRun:                 dbCfg.DryRun,
		TakerFeePct:            dbCfg.TakerFeePct,
	}
}

func (s *GormDBStore) SaveConfig(config models.BtcConfig) {
	pairsJoined := strings.Join(config.ScannerPairs, ",")
	dbCfg := models.DbAccountConfig{
		AccountID:              s.accountID,
		ExchangeKind:           string(s.exchange),
		Enabled:                config.Enabled,
		DryRun:                 config.DryRun,
		LlmActivationThreshold: config.LlmActivationThreshold,
		MinConfidence:          config.MinConfidence,
		MaxExposure:            config.MaxExposure,
		DailyLossLimitBtc:      config.DailyLossLimitBtc,
		MaxConsecutiveLosses:   config.MaxConsecutiveLosses,
		SafeModeVolatility:     config.SafeModeVolatility,
		SafeModeDrawdown:       config.SafeModeDrawdown,
		ScannerPairs:           pairsJoined,
		TakeProfitPct:          config.TakeProfitPct,
		StopLossPct:            config.StopLossPct,
		TrailingTpPct:          config.TrailingTpPct,
		UseTrailing:            config.UseTrailing,
		MaxPositions:           config.MaxPositions,
		RiskPerTradePct:        config.RiskPerTradePct,
		InitialCapitalUsdt:     config.InitialCapitalUsdt,
		MinScoreThreshold:      config.MinScoreThreshold,
		CompoundPct:            config.CompoundPct,
		TreasuryPct:            config.TreasuryPct,
		TakerFeePct:            config.TakerFeePct,
		UpdatedAt:              time.Now(),
	}

	if err := s.db.Save(&dbCfg).Error; err != nil {
		log.Printf("gorm: failed to save config: %v", err)
	}
}

func (s *GormDBStore) GetPositions() []models.BtcAdvisoryPosition {
	var dbPositions []models.DbOpenPosition
	err := s.db.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).Find(&dbPositions).Error
	if err != nil {
		log.Printf("gorm: failed to get positions: %v", err)
		return nil
	}

	positions := make([]models.BtcAdvisoryPosition, len(dbPositions))
	for i, p := range dbPositions {
		positions[i] = models.BtcAdvisoryPosition{
			ID:            p.ID,
			EntryPrice:    p.EntryPrice,
			CurrentPrice:  p.CurrentPrice,
			Size:          p.Size,
			PnlBtc:        p.PnlBtc,
			EntryTime:     p.EntryTime,
			Side:          p.Side,
			TakeProfitPct: p.TakeProfitPct,
			StopLossPct:   p.StopLossPct,
			TrailingTpPct: p.TrailingTpPct,
			UseTrailing:   p.UseTrailing,
			LlmTpReason:   p.LlmTpReason,
			LlmSlReason:   p.LlmSlReason,
			LlmConfidence: p.LlmConfidence,
			HighestPrice:  p.HighestPrice,
		}
	}
	return positions
}

func (s *GormDBStore) SavePositions(positions []models.BtcAdvisoryPosition) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Clear existing ones for this account / exchange
		if err := tx.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).Delete(&models.DbOpenPosition{}).Error; err != nil {
			return err
		}

		// Insert new ones
		for _, p := range positions {
			dbP := models.DbOpenPosition{
				ID:            p.ID,
				AccountID:     s.accountID,
				ExchangeKind:  string(s.exchange),
				EntryPrice:    p.EntryPrice,
				CurrentPrice:  p.CurrentPrice,
				Size:          p.Size,
				PnlBtc:        p.PnlBtc,
				EntryTime:     p.EntryTime,
				Side:          p.Side,
				TakeProfitPct: p.TakeProfitPct,
				StopLossPct:   p.StopLossPct,
				TrailingTpPct: p.TrailingTpPct,
				UseTrailing:   p.UseTrailing,
				LlmTpReason:   p.LlmTpReason,
				LlmSlReason:   p.LlmSlReason,
				LlmConfidence: p.LlmConfidence,
				HighestPrice:  p.HighestPrice,
				UpdatedAt:     time.Now(),
			}
			if err := tx.Save(&dbP).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("gorm: failed to save positions in transaction: %v", err)
	}
}

func (s *GormDBStore) GetLessons() []string {
	var dbLessons []models.DbTradingLesson
	err := s.db.Where("account_id = ? AND exchange_kind = ?", s.accountID, s.exchange).Order("id asc").Find(&dbLessons).Error
	if err != nil {
		log.Printf("gorm: failed to get lessons: %v", err)
		return nil
	}

	lessons := make([]string, len(dbLessons))
	for i, l := range dbLessons {
		lessons[i] = l.Lesson
	}
	return lessons
}

func (s *GormDBStore) AddLesson(lesson string) {
	dbLesson := models.DbTradingLesson{
		AccountID:    s.accountID,
		ExchangeKind: string(s.exchange),
		Lesson:       lesson,
		CreatedAt:    time.Now(),
	}

	if err := s.db.Create(&dbLesson).Error; err != nil {
		log.Printf("gorm: failed to create trading lesson: %v", err)
	}
}

func (s *GormDBStore) UpdateTreasuryOnClose(pair string, pnlPct, positionSizeQuote, btcPrice float64) bool {
	cfg := s.GetConfig()
	state := s.GetTreasuryState()
	pnlMultiplier := 1.0 + (pnlPct / 100.0)
	grossPnl := positionSizeQuote * (pnlMultiplier - 1.0)

	exitValue := positionSizeQuote * pnlMultiplier
	roundTripFee := (positionSizeQuote + exitValue) * cfg.TakerFeePct
	netPnl := grossPnl - roundTripFee

	isBtcQuote := math.Abs(btcPrice-1.0) < 1e-9

	if !isBtcQuote && btcPrice <= 0.0 {
		log.Printf("ERROR DB: Refusing to close %s — btc_price must be > 0 for USDT-quote pair (got %f). Fetch live BTCUSDT price before retrying.", pair, btcPrice)
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
		log.Printf("Position %s closed in DB at +%.2f%%. BTC treasury grew by %.8f BTC (profit: %.2f %s, fee: %.2f %s). Split: %.8f vault + %.8f compound",
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
		log.Printf("Position %s closed in DB at %.2f%%. BTC treasury reduced by %.8f BTC (loss: %.2f %s, fee: %.2f %s)",
			pair, pnlPct, math.Abs(btcDelta), math.Abs(grossPnl), unit, roundTripFee, unit)
	}

	s.SaveTreasuryState(state)
	s.UpdateGrowthRatios()
	return true
}

func (s *GormDBStore) LoadSkills() string {
	var dbSkill models.DbSystemSkill
	err := s.db.Where("key = ?", "default").First(&dbSkill).Error
	if err != nil {
		// Fetch local SKILL.md file contents and seed
		candidates := []string{"SKILL.md", "../SKILL.md", "/app/SKILL.md"}
		var content string
		for _, p := range candidates {
			if data, err := os.ReadFile(p); err == nil {
				content = string(data)
				break
			}
		}
		if content == "" {
			content = "# BTC Treasury Advisor (Spot)\n- Autonomous Binance spot scanner\n- Market regime detection\n- Risk assessment\n- LLM reasoning"
		}

		dbSkill = models.DbSystemSkill{
			Key:       "default",
			Content:   content,
			UpdatedAt: time.Now(),
		}
		_ = s.db.Save(&dbSkill)
		return content
	}

	return dbSkill.Content
}

func (s *GormDBStore) LoadLessonsContext() string {
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

// UpdateExchangeCredentials upserts API credentials into the account_exchanges table.
func (s *GormDBStore) UpdateExchangeCredentials(apiKey, apiSecret, passphrase string) error {
	result := s.db.Model(&models.DbAccountExchange{}).
		Where("account_id = ? AND exchange_kind = ?", s.accountID, string(s.exchange)).
		Updates(map[string]interface{}{
			"api_key":    apiKey,
			"api_secret": apiSecret,
			"passphrase": passphrase,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("credentials: db update failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// Row doesn't exist — create it
		row := models.DbAccountExchange{
			AccountID:    s.accountID,
			ExchangeKind: string(s.exchange),
			ApiKey:       apiKey,
			ApiSecret:    apiSecret,
			Passphrase:   passphrase,
			Enabled:      true,
		}
		if err := s.db.Create(&row).Error; err != nil {
			return fmt.Errorf("credentials: db create failed: %w", err)
		}
	}
	return nil
}
