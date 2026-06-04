package exchange

import (
	"fmt"
	"log"
	"os"

	"btc-treasury/internal/config"
)

type AccountKey struct {
	Exchange  config.ExchangeKind
	AccountID string
}

func AccountKeyFromSpec(spec *config.AccountSpec) AccountKey {
	return AccountKey{
		Exchange:  spec.Exchange,
		AccountID: spec.ID,
	}
}

type AccountSummary struct {
	Key           AccountKey
	Label         string
	Exchange      string
	APIKeyDisplay string
	Enabled       bool
}

type MultiExchangeClient struct {
	accounts   map[AccountKey]ExchangeClient
	defaultKey *AccountKey
}

func FromSpecs(specs []config.AccountSpec) *MultiExchangeClient {
	accounts := make(map[AccountKey]ExchangeClient)
	var defaultKey *AccountKey

	for _, spec := range specs {
		key := AccountKeyFromSpec(&spec)
		client, err := buildClientForSpec(&spec)
		if err != nil {
			log.Printf("MultiExchangeClient: skipping account %s — %v", spec.ID, err)
			continue
		}

		if defaultKey == nil {
			k := key
			defaultKey = &k
		}
		accounts[key] = client
	}

	return &MultiExchangeClient{
		accounts:   accounts,
		defaultKey: defaultKey,
	}
}

func (m *MultiExchangeClient) IsEmpty() bool {
	return len(m.accounts) == 0
}

func (m *MultiExchangeClient) Default() ExchangeClient {
	if m.defaultKey == nil {
		return nil
	}
	return m.accounts[*m.defaultKey]
}

func (m *MultiExchangeClient) ForAccount(key AccountKey) ExchangeClient {
	return m.accounts[key]
}

type Binding struct {
	Key    AccountKey
	Client ExchangeClient
}

func (m *MultiExchangeClient) ForAccountID(accountID string) []Binding {
	var results []Binding
	for k, c := range m.accounts {
		if k.AccountID == accountID {
			results = append(results, Binding{Key: k, Client: c})
		}
	}
	return results
}

func (m *MultiExchangeClient) List() []AccountSummary {
	var list []AccountSummary
	for k, client := range m.accounts {
		enabled := false
		if m.defaultKey != nil && *m.defaultKey == k {
			enabled = true
		}
		list = append(list, AccountSummary{
			Key:           k,
			Label:         k.AccountID,
			Exchange:      string(k.Exchange),
			APIKeyDisplay: client.APIKeyDisplay(),
			Enabled:       enabled,
		})
	}
	return list
}

func buildClientForSpec(spec *config.AccountSpec) (ExchangeClient, error) {
	apiKey, apiSecret, passphrase, err := spec.Credentials.Resolve()
	if err != nil {
		return nil, err
	}

	baseURL := os.Getenv("EXCHANGE_BASE_URL")

	switch spec.Exchange {
	case config.ExchangeBinance:
		client := NewBinanceClient(apiKey, apiSecret, baseURL)
		log.Printf("Binance client initialized (account=%s, api_key=%s)", spec.ID, client.APIKeyDisplay())
		return client, nil
	case config.ExchangeOkx:
		if passphrase == "" {
			return nil, fmt.Errorf("OKX account %s requires a passphrase", spec.ID)
		}
		client := NewOkxClient(apiKey, apiSecret, passphrase, baseURL)
		log.Printf("OKX client initialized (account=%s, api_key=%s)", spec.ID, client.APIKeyDisplay())
		return client, nil
	default:
		return nil, fmt.Errorf("unknown exchange kind %q", spec.Exchange)
	}
}

// fmt helper
type fmtError struct {
	msg string
}

func (f fmtError) Error() string { return f.msg }

func fmtErrorf(format string, a ...interface{}) error {
	return fmtError{msg: fmt.Sprintf(format, a...)}
}
