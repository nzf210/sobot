package models

import (
	"time"
)

// DbAccountSpec represents the GORM model for account specs
type DbAccountSpec struct {
	ID              string              `gorm:"primaryKey;size:50"`
	Label           string              `gorm:"size:100;not null"`
	TelegramChatIDs string              `gorm:"type:text;not null"` // JSON array e.g. "[339959699]"
	Enabled         bool                `gorm:"default:true;not null"`
	Exchanges       []DbAccountExchange `gorm:"foreignKey:AccountID;constraint:OnDelete:CASCADE;"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (DbAccountSpec) TableName() string { return "account_specs" }

// DbAccountExchange represents the GORM model for exchange configurations
type DbAccountExchange struct {
	AccountID     string    `gorm:"primaryKey;size:50"`
	ExchangeKind  string    `gorm:"primaryKey;size:20"` // "binance", "okx"
	ApiKey        string    `gorm:"type:text;not null"`
	ApiSecret     string    `gorm:"type:text;not null"`
	Passphrase    string    `gorm:"type:text"`
	ScannerPairs  string    `gorm:"type:text;not null"` // Comma-separated
	Enabled       bool      `gorm:"default:true;not null"`
	RiskOverrides string    `gorm:"type:text"` // JSON String of RiskOverrides
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (DbAccountExchange) TableName() string { return "account_exchanges" }

// DbAccountConfig represents the GORM model for account config
type DbAccountConfig struct {
	AccountID              string    `gorm:"primaryKey;size:50"`
	ExchangeKind           string    `gorm:"primaryKey;size:20"`
	Enabled                bool      `gorm:"default:true;not null"`
	DryRun                 bool      `gorm:"default:true;not null"`
	LlmActivationThreshold float64   `gorm:"not null"`
	MinConfidence          float64   `gorm:"not null"`
	MaxExposure            float64   `gorm:"not null"`
	DailyLossLimitBtc      float64   `gorm:"not null"`
	MaxConsecutiveLosses   int       `gorm:"not null"`
	SafeModeVolatility     float64   `gorm:"not null"`
	SafeModeDrawdown       float64   `gorm:"not null"`
	ScannerPairs           string    `gorm:"type:text;not null"` // Comma-separated
	TakeProfitPct          float64   `gorm:"not null"`
	StopLossPct            float64   `gorm:"not null"`
	TrailingTpPct          float64   `gorm:"not null"`
	UseTrailing            bool      `gorm:"default:true;not null"`
	MaxPositions           int       `gorm:"not null"`
	RiskPerTradePct        float64   `gorm:"not null"`
	InitialCapitalUsdt     float64   `gorm:"not null"`
	MinScoreThreshold      float64   `gorm:"not null"`
	CompoundPct            float64   `gorm:"not null"`
	TreasuryPct            float64   `gorm:"not null"`
	TakerFeePct            float64   `gorm:"not null"`
	UpdatedAt              time.Time
}

func (DbAccountConfig) TableName() string { return "account_configs" }

// DbTreasuryState represents the GORM model for treasury state
type DbTreasuryState struct {
	AccountID          string    `gorm:"primaryKey;size:50"`
	ExchangeKind       string    `gorm:"primaryKey;size:20"`
	CurrentBtc         float64   `gorm:"not null"`
	PreviousBtc        float64   `gorm:"not null"`
	BtcGrowth7d        float64   `gorm:"not null"`
	BtcGrowth30d       float64   `gorm:"not null"`
	StableValue        float64   `gorm:"not null"`
	UsdtBalance        float64   `gorm:"not null"`
	LastUpdate         string    `gorm:"size:50;not null"`
	BtcTreasuryVault   float64   `gorm:"not null"`
	CompoundBalance    float64   `gorm:"not null"`
	TotalTrades        int       `gorm:"not null"`
	WinningTrades      int       `gorm:"not null"`
	LosingTrades       int       `gorm:"not null"`
	TradingPausedUntil string    `gorm:"size:50"`
	ConsecutiveLosses  int       `gorm:"not null"`
	UpdatedAt          time.Time
}

func (DbTreasuryState) TableName() string { return "treasury_states" }

// DbOpenPosition represents the GORM model for open position
type DbOpenPosition struct {
	ID            string    `gorm:"primaryKey;size:50"`
	AccountID     string    `gorm:"size:50;index;not null"`
	ExchangeKind  string    `gorm:"size:20;index;not null"`
	EntryPrice    float64   `gorm:"not null"`
	CurrentPrice  float64   `gorm:"not null"`
	Size          float64   `gorm:"not null"`
	PnlBtc        float64   `gorm:"not null"`
	EntryTime     string    `gorm:"size:50;not null"`
	Side          string    `gorm:"size:10;not null"`
	TakeProfitPct float64   `gorm:"not null"`
	StopLossPct   float64   `gorm:"not null"`
	TrailingTpPct float64   `gorm:"not null"`
	UseTrailing   bool      `gorm:"not null"`
	LlmTpReason   string    `gorm:"type:text"`
	LlmSlReason   string    `gorm:"type:text"`
	LlmConfidence float64   `gorm:"not null"`
	HighestPrice  float64   `gorm:"not null"`
	UpdatedAt     time.Time
}

func (DbOpenPosition) TableName() string { return "open_positions" }

// DbDecisionLog represents the GORM model for decision log
type DbDecisionLog struct {
	ID           uint      `gorm:"primaryKey"`
	AccountID    string    `gorm:"size:50;index;not null"`
	ExchangeKind string    `gorm:"size:20;index;not null"`
	Timestamp    string    `gorm:"size:50;index;not null"`
	Pair         string    `gorm:"size:20;not null"`
	MarketRegime string    `gorm:"size:50;not null"`
	Confidence   float64   `gorm:"not null"`
	ActionTaken  string    `gorm:"size:50;not null"`
	RawRecord    string    `gorm:"type:text;not null"`
	CreatedAt    time.Time
}

func (DbDecisionLog) TableName() string { return "decision_logs" }

// DbTradingLesson represents the GORM model for trading lesson
type DbTradingLesson struct {
	ID           uint      `gorm:"primaryKey"`
	AccountID    string    `gorm:"size:50;index;not null"`
	ExchangeKind string    `gorm:"size:20;index;not null"`
	Lesson       string    `gorm:"type:text;not null"`
	CreatedAt    time.Time
}

func (DbTradingLesson) TableName() string { return "trading_lessons" }

// DbSystemSkill represents the GORM model for SKILL context
type DbSystemSkill struct {
	Key       string    `gorm:"primaryKey;size:50;default:'default'"`
	Content   string    `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

func (DbSystemSkill) TableName() string { return "system_skills" }
