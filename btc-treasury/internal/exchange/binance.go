package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"btc-treasury/internal/models"
)

type BinanceClient struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewBinanceClient(apiKey, apiSecret string, baseURL string) *BinanceClient {
	if baseURL == "" {
		baseURL = "https://api.binance.com"
	}
	return &BinanceClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *BinanceClient) ExchangeName() string {
	return "Binance"
}

func (c *BinanceClient) APIKeyDisplay() string {
	if len(c.apiKey) <= 8 {
		return "********"
	}
	return c.apiKey[:4] + "..." + c.apiKey[len(c.apiKey)-4:]
}

func (c *BinanceClient) sign(query string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(query))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *BinanceClient) timestamp() int64 {
	return time.Now().UnixMilli()
}

func (c *BinanceClient) publicGet(ctx context.Context, path string, query url.Values, target interface{}) error {
	return withRetry(fmt.Sprintf("GET %s", path), func() error {
		reqURL := fmt.Sprintf("%s%s?%s", c.baseURL, path, query.Encode())
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		}

		return json.NewDecoder(resp.Body).Decode(target)
	})
}

func (c *BinanceClient) signedGet(ctx context.Context, path string, params url.Values, target interface{}) error {
	return withRetry(fmt.Sprintf("GET %s", path), func() error {
		query := url.Values{}
		for k, v := range params {
			query[k] = v
		}
		query.Set("timestamp", strconv.FormatInt(c.timestamp(), 10))
		signature := c.sign(query.Encode())
		query.Set("signature", signature)

		reqURL := fmt.Sprintf("%s%s?%s", c.baseURL, path, query.Encode())
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-MBX-APIKEY", c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		}

		return json.NewDecoder(resp.Body).Decode(target)
	})
}

func (c *BinanceClient) signedPost(ctx context.Context, path string, params url.Values, target interface{}) error {
	// Trade orders are not retried to prevent double fills.
	query := url.Values{}
	for k, v := range params {
		query[k] = v
	}
	query.Set("timestamp", strconv.FormatInt(c.timestamp(), 10))
	signature := c.sign(query.Encode())
	query.Set("signature", signature)

	reqURL := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(query.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(target)
}

func (c *BinanceClient) signedDelete(ctx context.Context, path string, params url.Values, target interface{}) error {
	query := url.Values{}
	for k, v := range params {
		query[k] = v
	}
	query.Set("timestamp", strconv.FormatInt(c.timestamp(), 10))
	signature := c.sign(query.Encode())
	query.Set("signature", signature)

	reqURL := fmt.Sprintf("%s%s?%s", c.baseURL, path, query.Encode())
	req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(target)
}

type binanceBalanceRaw struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

type binanceAccountRaw struct {
	Balances []binanceBalanceRaw `json:"balances"`
}

func (c *BinanceClient) GetBalances(ctx context.Context) ([]models.ExchangeBalance, error) {
	var acct binanceAccountRaw
	err := c.signedGet(ctx, "/api/v3/account", url.Values{}, &acct)
	if err != nil {
		return nil, err
	}

	var balances []models.ExchangeBalance
	for _, b := range acct.Balances {
		free, _ := strconv.ParseFloat(b.Free, 64)
		locked, _ := strconv.ParseFloat(b.Locked, 64)
		if free > 0 || locked > 0 {
			balances = append(balances, models.ExchangeBalance{
				Asset:  b.Asset,
				Free:   free,
				Locked: locked,
			})
		}
	}
	return balances, nil
}

func (c *BinanceClient) GetOpenOrders(ctx context.Context, symbol string) ([]models.BtcAdvisoryPosition, error) {
	type binanceOrderRaw struct {
		Symbol   string `json:"symbol"`
		OrderID  int64  `json:"orderId"`
		Price    string `json:"price"`
		OrigQty  string `json:"origQty"`
		Side     string `json:"side"`
		Status   string `json:"status"`
	}

	var orders []binanceOrderRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	err := c.signedGet(ctx, "/api/v3/openOrders", params, &orders)
	if err != nil {
		return nil, err
	}

	var results []models.BtcAdvisoryPosition
	for _, o := range orders {
		price, _ := strconv.ParseFloat(o.Price, 64)
		qty, _ := strconv.ParseFloat(o.OrigQty, 64)
		results = append(results, models.BtcAdvisoryPosition{
			ID:            strconv.FormatInt(o.OrderID, 10),
			EntryPrice:    price,
			Size:          qty,
			Side:          o.Side,
		})
	}
	return results, nil
}

type binanceOrderResultRaw struct {
	Symbol  string `json:"symbol"`
	OrderID int64  `json:"orderId"`
	Status  string `json:"status"`
}

func (c *BinanceClient) PlaceMarketBuy(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error) {
	var res binanceOrderResultRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "MARKET")
	params.Set("quantity", strconv.FormatFloat(quantity, 'f', 8, 64))

	err := c.signedPost(ctx, "/api/v3/order", params, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	return models.ExchangeOrderResult{
		OrderID: strconv.FormatInt(res.OrderID, 10),
		Status:  res.Status,
	}, nil
}

func (c *BinanceClient) PlaceMarketBuyQuote(ctx context.Context, symbol string, quoteAmount float64) (models.ExchangeOrderResult, error) {
	var res binanceOrderResultRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "MARKET")
	params.Set("quoteOrderQty", strconv.FormatFloat(quoteAmount, 'f', 8, 64))

	err := c.signedPost(ctx, "/api/v3/order", params, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	return models.ExchangeOrderResult{
		OrderID: strconv.FormatInt(res.OrderID, 10),
		Status:  res.Status,
	}, nil
}

func (c *BinanceClient) PlaceLimitBuy(ctx context.Context, symbol string, quantity float64, price float64) (models.ExchangeOrderResult, error) {
	var res binanceOrderResultRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "LIMIT")
	params.Set("quantity", strconv.FormatFloat(quantity, 'f', 8, 64))
	params.Set("price", strconv.FormatFloat(price, 'f', 2, 64))
	params.Set("timeInForce", "GTC")

	err := c.signedPost(ctx, "/api/v3/order", params, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	return models.ExchangeOrderResult{
		OrderID: strconv.FormatInt(res.OrderID, 10),
		Status:  res.Status,
	}, nil
}

func (c *BinanceClient) PlaceMarketSell(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error) {
	var res binanceOrderResultRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "SELL")
	params.Set("type", "MARKET")
	params.Set("quantity", strconv.FormatFloat(quantity, 'f', 8, 64))

	err := c.signedPost(ctx, "/api/v3/order", params, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	return models.ExchangeOrderResult{
		OrderID: strconv.FormatInt(res.OrderID, 10),
		Status:  res.Status,
	}, nil
}

func (c *BinanceClient) CancelOrder(ctx context.Context, symbol string, orderID string) (models.ExchangeOrderResult, error) {
	var res binanceOrderResultRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("orderId", orderID)

	err := c.signedDelete(ctx, "/api/v3/order", params, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	return models.ExchangeOrderResult{
		OrderID: strconv.FormatInt(res.OrderID, 10),
		Status:  res.Status,
	}, nil
}

func (c *BinanceClient) CancelAll(ctx context.Context, symbol string) ([]models.ExchangeOrderResult, error) {
	orders, err := c.GetOpenOrders(ctx, symbol)
	if err != nil {
		return nil, err
	}

	var results []models.ExchangeOrderResult
	for _, o := range orders {
		res, err := c.CancelOrder(ctx, symbol, o.ID)
		if err == nil {
			results = append(results, res)
		}
	}
	return results, nil
}

type binanceExchangeInfoSymbol struct {
	Symbol string `json:"symbol"`
	Status string `json:"status"`
}

type binanceExchangeInfoRaw struct {
	Symbols []binanceExchangeInfoSymbol `json:"symbols"`
}

func (c *BinanceClient) ValidateSymbol(ctx context.Context, symbol string) (bool, error) {
	var info binanceExchangeInfoRaw
	err := c.publicGet(ctx, "/api/v3/exchangeInfo", url.Values{}, &info)
	if err != nil {
		return false, err
	}

	for _, s := range info.Symbols {
		if s.Symbol == symbol && s.Status == "TRADING" {
			return true, nil
		}
	}
	return false, nil
}

func (c *BinanceClient) DiscoverBtcPairs(ctx context.Context) ([]string, error) {
	var info binanceExchangeInfoRaw
	err := c.publicGet(ctx, "/api/v3/exchangeInfo", url.Values{}, &info)
	if err != nil {
		return nil, err
	}

	var pairs []string
	for _, s := range info.Symbols {
		if strings.HasSuffix(s.Symbol, "BTC") && s.Symbol != "BTCUSDT" && s.Status == "TRADING" {
			pairs = append(pairs, s.Symbol)
		}
	}
	return pairs, nil
}

func (c *BinanceClient) GetCurrentPrice(ctx context.Context, symbol string) (float64, error) {
	type priceResp struct {
		Price string `json:"price"`
	}
	var resp priceResp
	params := url.Values{}
	params.Set("symbol", symbol)
	err := c.publicGet(ctx, "/api/v3/ticker/price", params, &resp)
	if err != nil {
		return 0.0, err
	}
	return strconv.ParseFloat(resp.Price, 64)
}

func (c *BinanceClient) GetKlines(ctx context.Context, symbol string, interval string, limit uint32) ([]models.Ohlcv, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("interval", interval)
	params.Set("limit", strconv.FormatUint(uint64(limit), 10))

	var raw [][]interface{}
	err := c.publicGet(ctx, "/api/v3/klines", params, &raw)
	if err != nil {
		return nil, err
	}

	var klines []models.Ohlcv
	for _, row := range raw {
		if len(row) < 8 {
			continue
		}
		openTime, _ := row[0].(float64)
		open, _ := strconv.ParseFloat(row[1].(string), 64)
		high, _ := strconv.ParseFloat(row[2].(string), 64)
		low, _ := strconv.ParseFloat(row[3].(string), 64)
		closePrice, _ := strconv.ParseFloat(row[4].(string), 64)
		volume, _ := strconv.ParseFloat(row[5].(string), 64)
		quoteVol, _ := strconv.ParseFloat(row[7].(string), 64)

		klines = append(klines, models.Ohlcv{
			OpenTime:    int64(openTime),
			Open:        open,
			High:        high,
			Low:         low,
			Close:       closePrice,
			Volume:      volume,
			QuoteVolume: quoteVol,
		})
	}
	return klines, nil
}

type binanceTicker24h struct {
	PriceChangePercent string `json:"priceChangePercent"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
}

func (c *BinanceClient) GetMarketData(ctx context.Context, symbol string) (models.BtcMarketData, error) {
	// Retrieve L2 orderbook
	type depthRaw struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	var depth depthRaw
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("limit", "20")
	err := c.publicGet(ctx, "/api/v3/depth", params, &depth)
	if err != nil {
		return models.BtcMarketData{}, err
	}

	var bestBid, bestAsk, bidDepth, askDepth float64
	if len(depth.Bids) > 0 {
		bestBid, _ = strconv.ParseFloat(depth.Bids[0][0], 64)
		for _, b := range depth.Bids {
			sz, _ := strconv.ParseFloat(b[1], 64)
			bidDepth += sz
		}
	}
	if len(depth.Asks) > 0 {
		bestAsk, _ = strconv.ParseFloat(depth.Asks[0][0], 64)
		for _, a := range depth.Asks {
			sz, _ := strconv.ParseFloat(a[1], 64)
			askDepth += sz
		}
	}

	// Empty orderbook → transient connectivity issue; treat as error to avoid false SAFE_MODE
	if bestBid <= 0.0 || bestAsk <= 0.0 {
		return models.BtcMarketData{}, fmt.Errorf("empty orderbook for %s — skipping scan cycle", symbol)
	}

	spread := 0.0
	if bestAsk > 0.0 {
		spread = (bestAsk - bestBid) / bestAsk * 100.0
	}

	volumeScore := ((bidDepth + askDepth) / 100.0)
	if volumeScore > 10.0 {
		volumeScore = 10.0
	}
	
	minDepth := bidDepth
	if askDepth < minDepth {
		minDepth = askDepth
	}
	liquidityScore := minDepth / 50.0
	if liquidityScore > 10.0 {
		liquidityScore = 10.0
	}

	spreadScore := 10.0 - (spread * 20.0)
	if spreadScore > 10.0 {
		spreadScore = 10.0
	}
	if spreadScore < 0.0 {
		spreadScore = 0.0
	}

	totalVol := bidDepth + askDepth
	trendStrength := 0.0
	if totalVol > 0.0 {
		trendStrength = (bidDepth - askDepth) / totalVol * 10.0
	}

	confidence := 0.5
	if liquidityScore > 6.0 && spreadScore > 6.0 {
		confidence = 0.7
	}

	var ticker binanceTicker24h
	paramsTicker := url.Values{}
	paramsTicker.Set("symbol", symbol)
	_ = c.publicGet(ctx, "/api/v3/ticker/24hr", paramsTicker, &ticker)

	tickerChange, _ := strconv.ParseFloat(ticker.PriceChangePercent, 64)
	tickerVol, _ := strconv.ParseFloat(ticker.QuoteVolume, 64)
	tickerHigh, _ := strconv.ParseFloat(ticker.HighPrice, 64)
	tickerLow, _ := strconv.ParseFloat(ticker.LowPrice, 64)

	mid := 0.0
	if bestAsk > 0.0 && bestBid > 0.0 {
		mid = (bestAsk + bestBid) / 2.0
	}

	volatilityScore := 5.0
	if tickerHigh > 0.0 && tickerLow > 0.0 && mid > 0.0 {
		volatilityScore = ((tickerHigh - tickerLow) / mid * 100.0)
		if volatilityScore > 10.0 {
			volatilityScore = 10.0
		}
		if volatilityScore < 0.0 {
			volatilityScore = 0.0
		}
	}

	tickerVolScore := tickerVol / 50000000.0
	if tickerVolScore > 10.0 {
		tickerVolScore = 10.0
	}
	if tickerVolScore < 0.0 {
		tickerVolScore = 0.0
	}

	combinedVolume := volumeScore*0.5 + tickerVolScore*0.5

	breakoutProb := 0.3
	if mathAbs(tickerChange) > 5.0 && combinedVolume > 5.0 {
		breakoutProb = 0.65
	}

	reversalProb := 0.2
	if mathAbs(tickerChange) > 8.0 {
		reversalProb = 0.5
	}

	return models.BtcMarketData{
		Pair:                symbol,
		TrendStrength:       trendStrength,
		VolumeScore:         combinedVolume,
		LiquidityScore:      liquidityScore,
		SpreadScore:         spreadScore,
		VolatilityScore:     volatilityScore,
		BreakoutProbability: breakoutProb,
		ReversalProbability: reversalProb,
		Confidence:          confidence,
		ActiveStrategy:      "spot_accumulation",
	}, nil
}

func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func withRetry(opName string, op func() error) error {
	var lastErr error
	backoff := 1000 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientError(err) {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return lastErr
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "connection") || strings.Contains(msg, "429") || strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	return false
}
