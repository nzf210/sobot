package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"btc-treasury/internal/config"
	"btc-treasury/internal/models"
)

func runDbMigration(cfg *config.AppConfig) {
	if cfg.DBDsn == "" {
		log.Fatalf("Error: DB_DSN environment variable is not configured. Please set it in your .env file.")
	}

	log.Printf("Starting database migration to driver: %s...", cfg.DBDriver)

	var dialector gorm.Dialector
	if strings.ToLower(cfg.DBDriver) == "postgres" || strings.HasPrefix(cfg.DBDsn, "postgres://") || strings.HasPrefix(cfg.DBDsn, "postgresql://") {
		dialector = postgres.Open(cfg.DBDsn)
	} else {
		dialector = sqlite.Open(cfg.DBDsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to target database: %v", err)
	}

	// AutoMigrate all models
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
		log.Fatalf("AutoMigrate failed: %v", err)
	}

	log.Printf("Database schema migration completed successfully.")

	// Load specs from legacy JSON files (passing empty strings to DB parameters to force file load)
	accountSpecs, err := config.LoadAccountSpecs(cfg.ExchangeName, cfg.DataDir, cfg.ScannerPairs, "", "")
	if err != nil {
		log.Fatalf("Failed to load specs from files: %v", err)
	}

	log.Printf("Found %d account specifications in files to migrate.", len(accountSpecs))

	for _, spec := range accountSpecs {
		log.Printf("Migrating account: %s (Exchange: %s)", spec.ID, spec.Exchange)

		// 1. Migrate AccountSpec
		chatIDsBytes, _ := json.Marshal(spec.TelegramChatIDs)
		dbSpec := models.DbAccountSpec{
			ID:              spec.ID,
			Label:           strings.TrimSuffix(spec.Label, fmt.Sprintf(" (%s)", spec.Exchange)),
			TelegramChatIDs: string(chatIDsBytes),
			Enabled:         spec.Enabled,
		}

		if err := db.Save(&dbSpec).Error; err != nil {
			log.Printf("Warning: Failed to save spec %s: %v", spec.ID, err)
			continue
		}

		// 2. Migrate AccountExchange
		riskBytes, _ := json.Marshal(spec.Risk)
		dbExchange := models.DbAccountExchange{
			AccountID:     spec.ID,
			ExchangeKind:  string(spec.Exchange),
			ApiKey:        spec.Credentials.ApiKey,
			ApiSecret:     spec.Credentials.ApiSecret,
			Passphrase:    spec.Credentials.Passphrase,
			ScannerPairs:  strings.Join(spec.ScannerPairs, ","),
			Enabled:       spec.Enabled,
			RiskOverrides: string(riskBytes),
		}

		if err := db.Save(&dbExchange).Error; err != nil {
			log.Printf("Warning: Failed to save exchange credentials for %s: %v", spec.ID, err)
			continue
		}

		// 3. Migrate AccountConfig, TreasuryState, OpenPositions, DecisionLogs, TradingLessons
		isLegacyDefault := spec.ID == "" || spec.ID == "default"
		var accountDir string
		if isLegacyDefault {
			accountDir = cfg.DataDir
		} else {
			accountDir = filepath.Join(cfg.DataDir, "accounts", spec.ID, string(spec.Exchange))
			// Fallback to accounts/<id>
			if _, err := os.Stat(accountDir); os.IsNotExist(err) {
				accountDir = filepath.Join(cfg.DataDir, "accounts", spec.ID)
			}
		}

		log.Printf("Reading legacy JSON data files from: %s", accountDir)

		// btc-config.json
		cfgPath := filepath.Join(accountDir, "btc-config.json")
		if _, err := os.Stat(cfgPath); err == nil {
			if data, err := ioutil.ReadFile(cfgPath); err == nil {
				var fileCfg models.BtcConfig
				if err := json.Unmarshal(data, &fileCfg); err == nil {
					dbCfg := models.DbAccountConfig{
						AccountID:              spec.ID,
						ExchangeKind:           string(spec.Exchange),
						Enabled:                fileCfg.Enabled,
						DryRun:                 fileCfg.DryRun,
						LlmActivationThreshold: fileCfg.LlmActivationThreshold,
						MinConfidence:          fileCfg.MinConfidence,
						MaxExposure:            fileCfg.MaxExposure,
						DailyLossLimitBtc:      fileCfg.DailyLossLimitBtc,
						MaxConsecutiveLosses:   fileCfg.MaxConsecutiveLosses,
						SafeModeVolatility:     fileCfg.SafeModeVolatility,
						SafeModeDrawdown:       fileCfg.SafeModeDrawdown,
						ScannerPairs:           strings.Join(fileCfg.ScannerPairs, ","),
						TakeProfitPct:          fileCfg.TakeProfitPct,
						StopLossPct:            fileCfg.StopLossPct,
						TrailingTpPct:          fileCfg.TrailingTpPct,
						UseTrailing:            fileCfg.UseTrailing,
						MaxPositions:           fileCfg.MaxPositions,
						RiskPerTradePct:        fileCfg.RiskPerTradePct,
						InitialCapitalUsdt:     fileCfg.InitialCapitalUsdt,
						MinScoreThreshold:      fileCfg.MinScoreThreshold,
						CompoundPct:            fileCfg.CompoundPct,
						TreasuryPct:            fileCfg.TreasuryPct,
						TakerFeePct:            fileCfg.TakerFeePct,
						UpdatedAt:              time.Now(),
					}
					_ = db.Save(&dbCfg)
					log.Printf("Migrated btc-config.json for %s", spec.ID)
				}
			}
		}

		// btc-treasury.json
		treasuryPath := filepath.Join(accountDir, "btc-treasury.json")
		if _, err := os.Stat(treasuryPath); err == nil {
			if data, err := ioutil.ReadFile(treasuryPath); err == nil {
				var fileState models.BtcTreasuryState
				if err := json.Unmarshal(data, &fileState); err == nil {
					dbState := models.DbTreasuryState{
						AccountID:          spec.ID,
						ExchangeKind:       string(spec.Exchange),
						CurrentBtc:         fileState.CurrentBtc,
						PreviousBtc:        fileState.PreviousBtc,
						BtcGrowth7d:        fileState.BtcGrowth7d,
						BtcGrowth30d:       fileState.BtcGrowth30d,
						StableValue:        fileState.StableValue,
						UsdtBalance:        fileState.UsdtBalance,
						LastUpdate:         fileState.LastUpdate,
						BtcTreasuryVault:   fileState.BtcTreasuryVault,
						CompoundBalance:    fileState.CompoundBalance,
						TotalTrades:        fileState.TotalTrades,
						WinningTrades:      fileState.WinningTrades,
						LosingTrades:       fileState.LosingTrades,
						TradingPausedUntil: fileState.TradingPausedUntil,
						ConsecutiveLosses:  fileState.ConsecutiveLosses,
						UpdatedAt:          time.Now(),
					}
					_ = db.Save(&dbState)
					log.Printf("Migrated btc-treasury.json for %s", spec.ID)
				}
			}
		}

		// btc-positions.json
		posPath := filepath.Join(accountDir, "btc-positions.json")
		if _, err := os.Stat(posPath); err == nil {
			if data, err := ioutil.ReadFile(posPath); err == nil {
				var filePos []models.BtcAdvisoryPosition
				if err := json.Unmarshal(data, &filePos); err == nil {
					for _, p := range filePos {
						dbP := models.DbOpenPosition{
							ID:            p.ID,
							AccountID:     spec.ID,
							ExchangeKind:  string(spec.Exchange),
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
						_ = db.Save(&dbP)
					}
					log.Printf("Migrated %d open positions for %s", len(filePos), spec.ID)
				}
			}
		}

		// btc-decision-log.json
		decPath := filepath.Join(accountDir, "btc-decision-log.json")
		if _, err := os.Stat(decPath); err == nil {
			if data, err := ioutil.ReadFile(decPath); err == nil {
				var fileDec []models.BtcDecisionRecord
				if err := json.Unmarshal(data, &fileDec); err == nil {
					for _, d := range fileDec {
						rawBytes, _ := json.Marshal(d)
						dbDec := models.DbDecisionLog{
							AccountID:    spec.ID,
							ExchangeKind: string(spec.Exchange),
							Timestamp:    d.Timestamp,
							Pair:         d.MarketData.Pair,
							MarketRegime: d.MarketData.MarketRegime,
							Confidence:   d.Advisory.Confidence,
							ActionTaken:  d.ActionTaken,
							RawRecord:    string(rawBytes),
						}
						_ = db.Create(&dbDec)
					}
					log.Printf("Migrated %d decision logs for %s", len(fileDec), spec.ID)
				}
			}
		}

		// btc-lessons.json
		lesPath := filepath.Join(accountDir, "btc-lessons.json")
		if _, err := os.Stat(lesPath); err == nil {
			if data, err := ioutil.ReadFile(lesPath); err == nil {
				var fileLes []string
				if err := json.Unmarshal(data, &fileLes); err == nil {
					for _, l := range fileLes {
						dbLes := models.DbTradingLesson{
							AccountID:    spec.ID,
							ExchangeKind: string(spec.Exchange),
							Lesson:       l,
						}
						_ = db.Create(&dbLes)
					}
					log.Printf("Migrated %d lessons for %s", len(fileLes), spec.ID)
				}
			}
		}
	}

	// 4. Migrate SKILL.md to DB
	skillPath := filepath.Join(cfg.DataDir, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		if data, err := ioutil.ReadFile(skillPath); err == nil {
			dbSkill := models.DbSystemSkill{
				Key:       "default",
				Content:   string(data),
				UpdatedAt: time.Now(),
			}
			_ = db.Save(&dbSkill)
			log.Printf("Migrated SKILL.md to system_skills table in DB.")
		}
	}

	log.Println("Migration process completed successfully!")
}
