package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	MinLiquidityUSD          float64
	MaxPositions             int
	SniperSizeSOL            float64
	LLMEnabled               bool
	LLMURL                   string
	LLMAPIKey                string
	LLMModel                 string
	TelegramBotToken         string
	TelegramWhitelistUserIDs []string
	BackendPort              string
	ExecutorHost             string
	ExecutorPort             string
	RPCURL                   string
	WSSURL                   string
	LogLevel                 string
}

func Load() Config {
	_ = godotenv.Load()

	llmURL := getEnv("LLM_URL", "https://api.openai.com/v1")
	llmModel := getEnv("LLM_MODEL", "gpt-4o-mini")

	tgWhitelist := getEnv("TELEGRAM_WHITELIST_USER_IDS", "")
	if tgWhitelist == "" {
		tgWhitelist = os.Getenv("TELEGRAM_CHAT_ID")
	}

	var whitelistIDs []string
	if tgWhitelist != "" {
		for _, id := range strings.Split(tgWhitelist, ",") {
			trimmed := strings.TrimSpace(id)
			if trimmed != "" {
				whitelistIDs = append(whitelistIDs, trimmed)
			}
		}
	}

	return Config{
		MinLiquidityUSD:          getEnvFloat("MIN_LIQUIDITY_USD", 10000),
		MaxPositions:             getEnvInt("MAX_POSITIONS", 5),
		SniperSizeSOL:            getEnvFloat("SNIPER_SIZE_SOL", 0.1),
		LLMEnabled:               getEnvBool("LLM_ENABLED", true),
		LLMURL:                   llmURL,
		LLMAPIKey:                os.Getenv("LLM_API_KEY"),
		LLMModel:                 llmModel,
		TelegramBotToken:         os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramWhitelistUserIDs: whitelistIDs,
		BackendPort:              getEnv("BACKEND_PORT", "8080"),
		ExecutorHost:             getEnv("EXECUTOR_HOST", "localhost"),
		ExecutorPort:             getEnv("EXECUTOR_PORT", "3000"),
		RPCURL:                   getEnv("RPC_URL", "https://api.mainnet-beta.solana.com"),
		WSSURL:                   getEnv("WSS_URL", "wss://api.mainnet-beta.solana.com"),
		LogLevel:                 getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
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

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
