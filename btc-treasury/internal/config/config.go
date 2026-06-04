package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"btc-treasury/internal/utils"
)

type AppConfig struct {
	BackendPort           int
	LlmURL                string
	LlmAPIKey             string
	LlmModel              string
	DataDir               string
	TelegramBotToken      string
	TelegramWhitelist     []int64
	TelegramReportChatIDs []int64
	ExchangeName          string
	WalletPassword        string
	ScannerIntervalSecs   uint64
	ReportIntervalMins    uint64
	ScannerPairs          []string
}

func Load() *AppConfig {
	// Attempt to load .env from different directories up the tree
	_ = godotenv.Load()
	if _, err := os.Stat("../.env"); err == nil {
		_ = godotenv.Load("../.env")
	} else if _, err := os.Stat("../../../.env"); err == nil {
		_ = godotenv.Load("../../../.env")
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	rawDir := getEnv("DATA_BTC_DIR", getEnv("DATA_DIR", "../data/btc-treasury"))
	dataDir, err := utils.SanitizePath(rawDir, cwd)
	if err != nil {
		log.Printf("WARNING: %v — using default data directory", err)
		dataDir = filepath.Clean(filepath.Join(cwd, "../data/btc-treasury"))
	}

	scannerPairsRaw := getEnv("BTC_SCANNER_PAIRS", "")
	var scannerPairs []string
	if scannerPairsRaw != "" {
		for _, p := range strings.Split(scannerPairsRaw, ",") {
			pClean := strings.TrimSpace(strings.ToUpper(p))
			if pClean != "" {
				scannerPairs = append(scannerPairs, pClean)
			}
		}
	}
	if len(scannerPairs) == 0 {
		scannerPairs = []string{"ETHBTC", "SOLBTC"}
	}

	return &AppConfig{
		BackendPort:           getEnvInt("BTC_TREASURY_PORT", 8090),
		LlmURL:                getEnv("LLM_URL", "https://api.openai.com/v1"),
		LlmAPIKey:             os.Getenv("LLM_API_KEY"),
		LlmModel:              getEnv("LLM_MODEL", "gpt-4o-mini"),
		DataDir:               dataDir,
		TelegramBotToken:      getEnv("TELEGRAM_BOT_BTC_TOKEN", ""),
		TelegramWhitelist:     getEnvWhitelist("TELEGRAM_WHITELIST_USER_BTC_IDS"),
		TelegramReportChatIDs: getEnvWhitelist("TELEGRAM_REPORT_CHAT_IDS"),
		ExchangeName:          getEnv("EXCHANGE_NAME", "binance"),
		WalletPassword:        os.Getenv("WALLET_PASSWORD"),
		ScannerIntervalSecs:   uint64(getEnvInt("BTC_SCANNER_INTERVAL_SECS", 900)),
		ReportIntervalMins:    uint64(getEnvInt("BTC_REPORT_INTERVAL_MINS", 5)),
		ScannerPairs:          scannerPairs,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvWhitelist(key string) []int64 {
	var list []int64
	if v := os.Getenv(key); v != "" {
		for _, s := range strings.Split(v, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				list = append(list, id)
			}
		}
	}
	return list
}
