package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hybrid-solana-bot/internal/models"
)

type DecisionRecord struct {
	Timestamp string      `json:"timestamp"`
	Token     string      `json:"token"`
	Metrics   interface{} `json:"metrics"`
	Decision  string      `json:"decision"`
	Reasoning string      `json:"reasoning,omitempty"`
}

type UserConfig struct {
	AutoTrade             bool    `json:"autoTrade"`
	DryRun                bool    `json:"dryRun"`
	ScannerIntervalSec    int     `json:"scannerIntervalSec"`
	MinConfidence         float64 `json:"minConfidence"`
	MinLiquiditySOL       float64 `json:"minLiquiditySOL"`
	MaxLiquiditySOL       float64 `json:"maxLiquiditySOL"`
	MinVolumeSOL          float64 `json:"minVolumeSOL"`
	MinOrganicScore       float64 `json:"minOrganicScore"`
	MaxWashTradePct       float64 `json:"maxWashTradePct"`
	MinMcapSOL            float64 `json:"minMcapSOL"`
	MaxMcapSOL            float64 `json:"maxMcapSOL"`
	MaxTop10Pct           float64 `json:"maxTop10Pct"`
	MaxDeployAmountSol    float64 `json:"maxDeployAmountSol"`
	TakeProfitPct         float64 `json:"takeProfitPct"`
	StopLossPct           float64 `json:"stopLossPct"`
	TrailingTakeProfit    bool    `json:"trailingTakeProfit"`
	LLMTemperature        float64 `json:"llmTemperature"`
	MaxOpenPositions      int     `json:"maxOpenPositions"`
	DailyLossLimitUsd     float64 `json:"dailyLossLimitUsd"`
	MaxConsecutiveLosses  int     `json:"maxConsecutiveLosses"`
}

type MemoryStore struct {
	mu      sync.RWMutex
	dataDir string
}

func NewMemoryStore(dataDir string) *MemoryStore {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		panic(err)
	}
	store := &MemoryStore{dataDir: dataDir}
	store.initDefaults()
	return store
}

func (s *MemoryStore) initDefaults() {
	files := map[string]string{
		"decision-log.json":   `[]`,
		"lessons.json":        `[{"date":"2026-05-30","lesson":"Never buy tokens with >90% wash trade"}]`,
		"signal-weights.json": `{"liquidity": 0.4, "volume": 0.3, "organic_score": 0.3}`,
		"strategies.json":     `[{"name": "SnipeLowCap", "description": "Buy newly listed tokens with LP between 10k and 50k"}]`,
		"config.json":         `{"auto_trade": true}`,
		"user-config.json": `{
  "autoTrade": true,
  "dryRun": true,
  "llmTemperature": 0.2,
  "minConfidence": 0.70,
  "maxDeployAmountSol": 0.03,
  "minLiquiditySOL": 10,
  "maxLiquiditySOL": 300,
  "minVolumeSOL": 8,
  "minMcapSOL": 100,
  "maxMcapSOL": 5000,
  "maxTop10Pct": 50,
  "maxWashTradePct": 35,
  "minOrganicScore": 45,
  "scannerIntervalSec": 300,
  "stopLossPct": -10,
  "takeProfitPct": 20,
  "trailingTakeProfit": true,
  "maxOpenPositions": 3,
  "dailyLossLimitUsd": 2,
  "maxConsecutiveLosses": 3
}`,
		"pool-memory.json":    `[]`,
		"SKILL.md":            "# Bot Skills\n- Sniping\n- Risk Analysis\n- Auto-learning",
	}

	for filename, defaultContent := range files {
		path := filepath.Join(s.dataDir, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.WriteFile(path, []byte(defaultContent), 0644)
		}
	}
}

func (s *MemoryStore) LogDecision(token string, metrics interface{}, decision, reasoning string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dataDir, "decision-log.json")
	
	var records []DecisionRecord
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &records)
	}

	records = append(records, DecisionRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Token:     token,
		Metrics:   metrics,
		Decision:  decision,
		Reasoning: reasoning,
	})

	out, _ := json.MarshalIndent(records, "", "  ")
	return os.WriteFile(path, out, 0644)
}

func (s *MemoryStore) LoadContext() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ctx string
	
	// Read strategies
	if b, err := os.ReadFile(filepath.Join(s.dataDir, "strategies.json")); err == nil {
		ctx += "\nSTRATEGIES:\n" + string(b)
	}
	
	// Read lessons
	if b, err := os.ReadFile(filepath.Join(s.dataDir, "lessons.json")); err == nil {
		ctx += "\nLESSONS LEARNED:\n" + string(b)
	}

	// Read signal weights
	if b, err := os.ReadFile(filepath.Join(s.dataDir, "signal-weights.json")); err == nil {
		ctx += "\nSIGNAL WEIGHTS:\n" + string(b)
	}

	// Read user config
	if b, err := os.ReadFile(filepath.Join(s.dataDir, "user-config.json")); err == nil {
		ctx += "\nUSER CONFIG:\n" + string(b)
	}

	return ctx
}

func (s *MemoryStore) GetUserConfig() UserConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var cfg UserConfig
	path := filepath.Join(s.dataDir, "user-config.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &cfg)
	}
	return cfg
}

func (s *MemoryStore) UpdateUserConfig(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dataDir, "user-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var cfgMap map[string]interface{}
	if err := json.Unmarshal(data, &cfgMap); err != nil {
		return err
	}

	cfgMap[key] = value

	out, err := json.MarshalIndent(cfgMap, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0644)
}
func (s *MemoryStore) GetPositions() []models.Position {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var positions []models.Position
	path := filepath.Join(s.dataDir, "pool-memory.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &positions)
	}
	return positions
}

func (s *MemoryStore) SavePositions(positions []models.Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dataDir, "pool-memory.json")
	out, _ := json.MarshalIndent(positions, "", "  ")
	return os.WriteFile(path, out, 0644)
}

func (s *MemoryStore) AddLesson(lesson string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dataDir, "lessons.json")
	var lessons []map[string]string
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &lessons)
	}

	lessons = append(lessons, map[string]string{
		"date": time.Now().UTC().Format(time.RFC3339),
		"lesson": lesson,
	})

	out, _ := json.MarshalIndent(lessons, "", "  ")
	return os.WriteFile(path, out, 0644)
}
