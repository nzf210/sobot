package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"btc-treasury/internal/models"
)

type ExchangeKind string

const (
	ExchangeBinance ExchangeKind = "binance"
	ExchangeOkx     ExchangeKind = "okx"
)

func ParseExchangeKind(s string) (ExchangeKind, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "binance":
		return ExchangeBinance, nil
	case "okx":
		return ExchangeOkx, nil
	default:
		return "", fmt.Errorf("unknown exchange kind %q", s)
	}
}

type Credentials struct {
	ApiKey        string
	ApiSecret     string
	Passphrase    string
	KeyEnv        string
	SecretEnv     string
	PassphraseEnv string
}

func (c *Credentials) Resolve() (string, string, string, error) {
	if c.KeyEnv != "" && c.SecretEnv != "" {
		key := strings.TrimSpace(os.Getenv(c.KeyEnv))
		if key == "" {
			return "", "", "", fmt.Errorf("env var %s not set or empty", c.KeyEnv)
		}
		secret := strings.TrimSpace(os.Getenv(c.SecretEnv))
		if secret == "" {
			return "", "", "", fmt.Errorf("env var %s not set or empty", c.SecretEnv)
		}
		var passphrase string
		if c.PassphraseEnv != "" {
			passphrase = strings.TrimSpace(os.Getenv(c.PassphraseEnv))
		}
		return key, secret, passphrase, nil
	}

	if c.ApiKey == "" || c.ApiSecret == "" {
		return "", "", "", errors.New("empty inline credentials")
	}
	return c.ApiKey, c.ApiSecret, c.Passphrase, nil
}

type RiskOverrides struct {
	RiskPerTradePct      *float64 `json:"risk_per_trade_pct,omitempty"`
	MaxPositions         *int     `json:"max_positions,omitempty"`
	DailyLossLimitBtc    *float64 `json:"daily_loss_limit_btc,omitempty"`
	MaxConsecutiveLosses *int     `json:"max_consecutive_losses,omitempty"`
	TakeProfitPct        *float64 `json:"take_profit_pct,omitempty"`
	StopLossPct          *float64 `json:"stop_loss_pct,omitempty"`
	TrailingTpPct        *float64 `json:"trailing_tp_pct,omitempty"`
}

type AccountSpec struct {
	ID               string
	Label            string
	Exchange         ExchangeKind
	Credentials      Credentials
	ScannerPairs     []string
	TelegramChatIDs  []int64
	Risk             RiskOverrides
	Enabled          bool
}

type accountsConfigRaw struct {
	Accounts []accountEntryRaw `json:"accounts"`
}

type accountEntryRaw struct {
	ID              string             `json:"id"`
	Label           *string            `json:"label"`
	TelegramChatIDs []int64            `json:"telegram_chat_ids"`
	Exchanges       []exchangeEntryRaw `json:"exchanges"`
}

type exchangeEntryRaw struct {
	Kind         *string        `json:"kind"`
	ApiKey       *string        `json:"api_key"`
	ApiSecret    *string        `json:"api_secret"`
	Passphrase   *string        `json:"passphrase"`
	ScannerPairs []string       `json:"scanner_pairs"`
	Enabled      *bool          `json:"enabled"`
	Risk         *RiskOverrides `json:"risk"`
}

func LoadAccountSpecs(exchangeName string, dataDir string, scannerPairs []string, dbDriver string, dbDsn string) ([]AccountSpec, error) {
	// 0. Database spec loading if DSN is set
	if dbDsn != "" {
		specs, err := LoadAccountSpecsFromDB(dbDriver, dbDsn)
		if err == nil && len(specs) > 0 {
			return specs, nil
		}
		if err != nil {
			logError(fmt.Sprintf("Database spec load failed: %v. Falling back to files...", err))
		}
	}

	// 1. Explicit env-var JSON
	if jsonStr := strings.TrimSpace(os.Getenv("BTC_ACCOUNTS_JSON")); jsonStr != "" {
		specs, err := LoadAccountSpecsFromJSON(jsonStr)
		if err == nil {
			return specs, nil
		}
		logError(fmt.Sprintf("BTC_ACCOUNTS_JSON parse failed: %v", err))
	}

	// 2. Per-account JSON file.
	if defaultJsonDir := getEnv("DATA_BTC_DIR", os.Getenv("DATA_DIR")); defaultJsonDir != "" {
		flatPath := filepath.Join(defaultJsonDir, "btc-accounts.json")
		if _, err := os.Stat(flatPath); err == nil {
			if data, err := os.ReadFile(flatPath); err == nil {
				specs, err := LoadAccountSpecsFromJSON(string(data))
				if err == nil {
					return specs, nil
				}
				logError(fmt.Sprintf("btc-accounts.json parse failed: %v", err))
			}
		}

		accountsDir := filepath.Join(defaultJsonDir, "accounts")
		if info, err := os.Stat(accountsDir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(accountsDir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						path := filepath.Join(accountsDir, entry.Name(), "accounts.json")
						if _, err := os.Stat(path); err == nil {
							if data, err := os.ReadFile(path); err == nil {
								specs, err := LoadAccountSpecsFromJSON(string(data))
								if err == nil {
									return specs, nil
								}
								logError(fmt.Sprintf("%s parse failed: %v", path, err))
							}
						}
					}
				}
			}
		}
	}

	// 3. Legacy env-var fallback
	return LegacyDefaultSpecs(exchangeName, scannerPairs), nil
}

func LoadAccountSpecsFromDB(driver, dsn string) ([]AccountSpec, error) {
	var dialector gorm.Dialector
	if strings.ToLower(driver) == "postgres" || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dialector = postgres.Open(dsn)
	} else {
		dialector = sqlite.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// AutoMigrate just to ensure the specs tables exist
	_ = db.AutoMigrate(&models.DbAccountSpec{}, &models.DbAccountExchange{})

	var dbSpecs []models.DbAccountSpec
	err = db.Preload("Exchanges").Find(&dbSpecs).Error
	if err != nil {
		return nil, err
	}

	var specs []AccountSpec
	for _, spec := range dbSpecs {
		if !spec.Enabled {
			continue
		}
		var chatIDs []int64
		if spec.TelegramChatIDs != "" {
			_ = json.Unmarshal([]byte(spec.TelegramChatIDs), &chatIDs)
		}

		for _, ex := range spec.Exchanges {
			if !ex.Enabled {
				continue
			}
			kind, err := ParseExchangeKind(ex.ExchangeKind)
			if err != nil {
				continue
			}

			var pairs []string
			if ex.ScannerPairs != "" {
				for _, p := range strings.Split(ex.ScannerPairs, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						pairs = append(pairs, p)
					}
				}
			}

			var risk RiskOverrides
			if ex.RiskOverrides != "" {
				_ = json.Unmarshal([]byte(ex.RiskOverrides), &risk)
			}

			specs = append(specs, AccountSpec{
				ID:    spec.ID,
				Label: fmt.Sprintf("%s (%s)", spec.Label, kind),
				Exchange: kind,
				Credentials: Credentials{
					ApiKey:     ex.ApiKey,
					ApiSecret:  ex.ApiSecret,
					Passphrase: ex.Passphrase,
				},
				ScannerPairs:    pairs,
				TelegramChatIDs: chatIDs,
				Risk:            risk,
				Enabled:         ex.Enabled,
			})
		}
	}
	return specs, nil
}

func LoadAccountSpecsFromJSON(jsonStr string) ([]AccountSpec, error) {
	var raw accountsConfigRaw
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("accounts JSON: %w", err)
	}

	var specs []AccountSpec
	for _, acct := range raw.Accounts {
		id := strings.TrimSpace(acct.ID)
		if id == "" {
			return nil, errors.New("account id is empty in accounts JSON")
		}
		label := id
		if acct.Label != nil {
			label = *acct.Label
		}
		chatIDs := acct.TelegramChatIDs

		for _, ex := range acct.Exchanges {
			if ex.Kind == nil {
				return nil, fmt.Errorf("missing 'kind' in exchange for account %s", id)
			}
			kind, err := ParseExchangeKind(*ex.Kind)
			if err != nil {
				return nil, fmt.Errorf("unknown exchange kind %q for account %s: %w", *ex.Kind, id, err)
			}

			if ex.ApiKey == nil || ex.ApiSecret == nil {
				return nil, fmt.Errorf("missing api_key/api_secret for %s/%s", id, *ex.Kind)
			}
			apiKey := strings.TrimSpace(*ex.ApiKey)
			apiSecret := strings.TrimSpace(*ex.ApiSecret)
			if apiKey == "" || apiSecret == "" {
				return nil, fmt.Errorf("empty credentials for %s/%s", id, *ex.Kind)
			}

			var passphrase string
			if ex.Passphrase != nil {
				passphrase = *ex.Passphrase
			}
			if kind == ExchangeOkx && passphrase == "" {
				return nil, fmt.Errorf("OKX account %s requires a passphrase", id)
			}

			enabled := true
			if ex.Enabled != nil {
				enabled = *ex.Enabled
			}

			var risk RiskOverrides
			if ex.Risk != nil {
				risk = *ex.Risk
			}

			specs = append(specs, AccountSpec{
				ID:    id,
				Label: fmt.Sprintf("%s (%s)", label, kind),
				Exchange: kind,
				Credentials: Credentials{
					ApiKey:     apiKey,
					ApiSecret:  apiSecret,
					Passphrase: passphrase,
				},
				ScannerPairs:    ex.ScannerPairs,
				TelegramChatIDs: chatIDs,
				Risk:            risk,
				Enabled:         enabled,
			})
		}
	}
	return specs, nil
}

func LegacyDefaultSpecs(exchangeName string, scannerPairs []string) []AccountSpec {
	lowered := strings.TrimSpace(strings.ToLower(exchangeName))
	var names []string
	if lowered == "both" {
		names = []string{"binance", "okx"}
	} else {
		for _, name := range strings.Split(lowered, ",") {
			n := strings.TrimSpace(name)
			if n != "" {
				names = append(names, n)
			}
		}
	}

	var specs []AccountSpec
	for _, name := range names {
		if spec := legacyDefaultSpec(name, scannerPairs); spec != nil {
			specs = append(specs, *spec)
		}
	}
	return specs
}

func legacyDefaultSpec(exchangeName string, scannerPairs []string) *AccountSpec {
	kind, err := ParseExchangeKind(exchangeName)
	if err != nil {
		return nil
	}

	var keyEnv, secretEnv string
	var passphraseEnv string

	switch kind {
	case ExchangeBinance:
		keyEnv = "BINANCE_API_KEY"
		secretEnv = "BINANCE_API_SECRET"
	case ExchangeOkx:
		keyEnv = "OKX_API_KEY"
		secretEnv = "OKX_API_SECRET"
		passphraseEnv = "OKX_API_PASSPHRASE"
	}

	resolvedKeyEnv := ""
	if os.Getenv(keyEnv) != "" {
		resolvedKeyEnv = keyEnv
	} else if os.Getenv("EXCHANGE_API_KEY") != "" {
		resolvedKeyEnv = "EXCHANGE_API_KEY"
	} else {
		return nil
	}

	resolvedSecretEnv := ""
	if os.Getenv(secretEnv) != "" {
		resolvedSecretEnv = secretEnv
	} else if os.Getenv("EXCHANGE_API_SECRET") != "" {
		resolvedSecretEnv = "EXCHANGE_API_SECRET"
	} else {
		return nil
	}

	if passphraseEnv != "" {
		if os.Getenv(passphraseEnv) == "" {
			return nil
		}
	}

	return &AccountSpec{
		ID:    "default",
		Label: fmt.Sprintf("Default %s", strings.ToUpper(exchangeName)),
		Exchange: kind,
		Credentials: Credentials{
			KeyEnv:        resolvedKeyEnv,
			SecretEnv:     resolvedSecretEnv,
			PassphraseEnv: passphraseEnv,
		},
		ScannerPairs:    scannerPairs,
		TelegramChatIDs: nil,
		Risk:            RiskOverrides{},
		Enabled:         true,
	}
}

func ValidateSpecs(specs []AccountSpec) error {
	seen := make(map[string]bool)
	for _, s := range specs {
		if s.ID == "" {
			return errors.New("account id is empty")
		}
		key := fmt.Sprintf("%s|%s", s.ID, s.Exchange)
		if seen[key] {
			return fmt.Errorf("duplicate (id, exchange) pair: (%s, %s)", s.ID, s.Exchange)
		}
		seen[key] = true

		if s.Enabled {
			if _, _, _, err := s.Credentials.Resolve(); err != nil {
				return fmt.Errorf("account %s on %s enabled but credentials unresolved: %w", s.ID, s.Exchange, err)
			}
		}
	}
	return nil
}

func logError(msg string) {
	fmt.Fprintf(os.Stderr, "ERROR: %s\n", msg)
}
