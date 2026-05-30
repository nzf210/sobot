package config

import (
	"os"
	"strings"
)

type Config struct {
    MinLiquidityUSD float64
    MaxPositions int
    SniperSizeSOL float64
    LLMEnabled bool
    LLMURL string
    LLMAPIKey string
    LLMModel string
    TelegramBotToken string
    TelegramWhitelistUserIDs []string
}

func Load() Config {
    llmURL := os.Getenv("LLM_URL")
    if llmURL == "" {
        llmURL = "https://api.openai.com/v1" // Default fallback
    }

    tgWhitelist := os.Getenv("TELEGRAM_WHITELIST_USER_IDS")
    if tgWhitelist == "" {
        // Fallback to TELEGRAM_CHAT_ID for backward compatibility
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
        MinLiquidityUSD: 10000,
        MaxPositions: 5,
        SniperSizeSOL: 0.1,
        LLMEnabled: true,
        LLMURL: llmURL,
        LLMAPIKey: os.Getenv("LLM_API_KEY"),
        LLMModel: os.Getenv("LLM_MODEL"),
        TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
        TelegramWhitelistUserIDs: whitelistIDs,
    }
}