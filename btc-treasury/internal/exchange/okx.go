package exchange

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"btc-treasury/internal/models"
)

type OkxClient struct {
	baseURL        string
	apiKey         string
	apiSecret      string
	passphrase     string
	httpClient     *http.Client
	limiterTrade   *rate.Limiter
	limiterMarket  *rate.Limiter
	limiterAccount *rate.Limiter
}

func NewOkxClient(apiKey, apiSecret, passphrase string, baseURL string) *OkxClient {
	if baseURL == "" {
		baseURL = "https://www.okx.com"
	}
	return &OkxClient{
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		apiKey:         apiKey,
		apiSecret:      apiSecret,
		passphrase:     passphrase,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		limiterTrade:   rate.NewLimiter(rate.Limit(30), 60),  // 60 req / 2s
		limiterMarket:  rate.NewLimiter(rate.Limit(10), 20),  // 20 req / 2s
		limiterAccount: rate.NewLimiter(rate.Limit(5), 10),   // 10 req / 2s
	}
}

func (c *OkxClient) ExchangeName() string {
	return "OKX"
}

func (c *OkxClient) APIKeyDisplay() string {
	if len(c.apiKey) <= 8 {
		return "********"
	}
	return c.apiKey[:4] + "..." + c.apiKey[len(c.apiKey)-4:]
}

func (c *OkxClient) toOkxInstID(symbol string) (string, error) {
	s := strings.TrimSpace(strings.ToUpper(symbol))
	if s == "" {
		return "", errors.New("empty pair")
	}
	quotes := []string{"USDT", "USDC", "BTC"}
	for _, quote := range quotes {
		if strings.HasSuffix(s, quote) {
			base := strings.TrimSuffix(s, quote)
			if base == "" {
				return "", fmt.Errorf("empty base ccy in pair %s", s)
			}
			return fmt.Sprintf("%s-%s", base, quote), nil
		}
	}
	return "", fmt.Errorf("unknown quote ccy in pair %s", s)
}

func (c *OkxClient) sign(timestamp, method, requestPath, body string) string {
	signString := timestamp + method + requestPath + body
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(signString))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (c *OkxClient) okxTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func (c *OkxClient) acquire(ctx context.Context, bucket string) error {
	var l *rate.Limiter
	switch bucket {
	case "trade":
		l = c.limiterTrade
	case "market":
		l = c.limiterMarket
	case "account":
		l = c.limiterAccount
	default:
		return nil
	}
	return l.Wait(ctx)
}

type okxEnvelopeRaw struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *OkxClient) publicGet(ctx context.Context, path string, query string, target interface{}) error {
	return withRetry(fmt.Sprintf("GET %s", path), func() error {
		if err := c.acquire(ctx, "market"); err != nil {
			return err
		}

		reqURL := c.baseURL + path
		if query != "" {
			reqURL += "?" + query
		}

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

		var envelope okxEnvelopeRaw
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return err
		}

		if envelope.Code != "" && envelope.Code != "0" {
			return fmt.Errorf("OKX API error: code=%s msg=%s", envelope.Code, envelope.Msg)
		}

		return json.Unmarshal(envelope.Data, target)
	})
}

func (c *OkxClient) signedGet(ctx context.Context, bucket string, path string, query string, target interface{}) error {
	return withRetry(fmt.Sprintf("GET %s", path), func() error {
		if err := c.acquire(ctx, bucket); err != nil {
			return err
		}

		requestPath := path
		if query != "" {
			requestPath += "?" + query
		}

		timestamp := c.okxTimestamp()
		signature := c.sign(timestamp, "GET", requestPath, "")

		reqURL := c.baseURL + requestPath
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return err
		}

		req.Header.Set("OK-ACCESS-KEY", c.apiKey)
		req.Header.Set("OK-ACCESS-SIGN", signature)
		req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
		req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		}

		var envelope okxEnvelopeRaw
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return err
		}

		if envelope.Code != "" && envelope.Code != "0" {
			return fmt.Errorf("OKX API error: code=%s msg=%s", envelope.Code, envelope.Msg)
		}

		return json.Unmarshal(envelope.Data, target)
	})
}

func (c *OkxClient) signedPost(ctx context.Context, bucket string, path string, bodyJSON string, target interface{}) error {
	if err := c.acquire(ctx, bucket); err != nil {
		return err
	}

	timestamp := c.okxTimestamp()
	signature := c.sign(timestamp, "POST", path, bodyJSON)

	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader([]byte(bodyJSON)))
	if err != nil {
		return err
	}

	req.Header.Set("OK-ACCESS-KEY", c.apiKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	var envelope okxEnvelopeRaw
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}

	if envelope.Code != "" && envelope.Code != "0" {
		return fmt.Errorf("OKX API error: code=%s msg=%s", envelope.Code, envelope.Msg)
	}

	return json.Unmarshal(envelope.Data, target)
}

type okxBalanceDetailRaw struct {
	Ccy       string `json:"ccy"`
	AvailBal  string `json:"availBal"`
	FrozenBal string `json:"frozenBal"`
}

type okxAccountRaw struct {
	Details []okxBalanceDetailRaw `json:"details"`
}

func (c *OkxClient) GetBalances(ctx context.Context) ([]models.ExchangeBalance, error) {
	var details []okxAccountRaw
	err := c.signedGet(ctx, "account", "/api/v5/account/balance", "", &details)
	if err != nil {
		return nil, err
	}

	var balances []models.ExchangeBalance
	for _, detailsRaw := range details {
		for _, b := range detailsRaw.Details {
			free, _ := strconv.ParseFloat(b.AvailBal, 64)
			locked, _ := strconv.ParseFloat(b.FrozenBal, 64)
			if free > 0 || locked > 0 {
				balances = append(balances, models.ExchangeBalance{
					Asset:  b.Ccy,
					Free:   free,
					Locked: locked,
				})
			}
		}
	}
	return balances, nil
}

type okxPendingOrderRaw struct {
	OrdID  string `json:"ordId"`
	InstID string `json:"instId"`
	Side   string `json:"side"`
	AvgPx  string `json:"avgPx"`
	Sz     string `json:"sz"`
}

func (c *OkxClient) GetOpenOrders(ctx context.Context, symbol string) ([]models.BtcAdvisoryPosition, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return nil, err
	}

	var orders []okxPendingOrderRaw
	err = c.signedGet(ctx, "account", "/api/v5/trade/orders-pending", "instId="+instID, &orders)
	if err != nil {
		return nil, err
	}

	var results []models.BtcAdvisoryPosition
	for _, o := range orders {
		price, _ := strconv.ParseFloat(o.AvgPx, 64)
		qty, _ := strconv.ParseFloat(o.Sz, 64)
		results = append(results, models.BtcAdvisoryPosition{
			ID:         o.OrdID,
			EntryPrice: price,
			Size:       qty,
			Side:       strings.ToUpper(o.Side),
		})
	}
	return results, nil
}

type okxOrderResultRaw struct {
	OrdID string `json:"ordId"`
	SCode string `json:"sCode"`
	SMsg  string `json:"sMsg"`
}

func (c *OkxClient) PlaceMarketBuy(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	body := fmt.Sprintf(`{"instId":"%s","side":"buy","ordType":"market","sz":"%.8f","tgtCcy":"base_ccy"}`, instID, quantity)
	var res []okxOrderResultRaw
	err = c.signedPost(ctx, "trade", "/api/v5/trade/order", body, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	if len(res) == 0 {
		return models.ExchangeOrderResult{}, errors.New("empty order response")
	}

	return models.ExchangeOrderResult{
		OrderID: res[0].OrdID,
		Status:  "submitted",
	}, nil
}

func (c *OkxClient) PlaceMarketBuyQuote(ctx context.Context, symbol string, quoteAmount float64) (models.ExchangeOrderResult, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	body := fmt.Sprintf(`{"instId":"%s","side":"buy","ordType":"market","sz":"%.8f","tgtCcy":"quote_ccy"}`, instID, quoteAmount)
	var res []okxOrderResultRaw
	err = c.signedPost(ctx, "trade", "/api/v5/trade/order", body, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	if len(res) == 0 {
		return models.ExchangeOrderResult{}, errors.New("empty order response")
	}

	return models.ExchangeOrderResult{
		OrderID: res[0].OrdID,
		Status:  "submitted",
	}, nil
}

func (c *OkxClient) PlaceLimitBuy(ctx context.Context, symbol string, quantity float64, price float64) (models.ExchangeOrderResult, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	body := fmt.Sprintf(`{"instId":"%s","side":"buy","ordType":"limit","sz":"%.8f","px":"%.8f","tgtCcy":"base_ccy"}`, instID, quantity, price)
	var res []okxOrderResultRaw
	err = c.signedPost(ctx, "trade", "/api/v5/trade/order", body, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	if len(res) == 0 {
		return models.ExchangeOrderResult{}, errors.New("empty order response")
	}

	return models.ExchangeOrderResult{
		OrderID: res[0].OrdID,
		Status:  "submitted",
	}, nil
}

func (c *OkxClient) PlaceMarketSell(ctx context.Context, symbol string, quantity float64) (models.ExchangeOrderResult, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	body := fmt.Sprintf(`{"instId":"%s","side":"sell","ordType":"market","sz":"%.8f","tgtCcy":"base_ccy"}`, instID, quantity)
	var res []okxOrderResultRaw
	err = c.signedPost(ctx, "trade", "/api/v5/trade/order", body, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	if len(res) == 0 {
		return models.ExchangeOrderResult{}, errors.New("empty order response")
	}

	return models.ExchangeOrderResult{
		OrderID: res[0].OrdID,
		Status:  "submitted",
	}, nil
}

func (c *OkxClient) CancelOrder(ctx context.Context, symbol string, orderID string) (models.ExchangeOrderResult, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	body := fmt.Sprintf(`{"instId":"%s","ordId":"%s"}`, instID, orderID)
	var res []okxOrderResultRaw
	err = c.signedPost(ctx, "trade", "/api/v5/trade/cancel-order", body, &res)
	if err != nil {
		return models.ExchangeOrderResult{}, err
	}

	if len(res) == 0 {
		return models.ExchangeOrderResult{}, errors.New("empty cancel response")
	}

	return models.ExchangeOrderResult{
		OrderID: res[0].OrdID,
		Status:  "cancelled",
	}, nil
}

func (c *OkxClient) CancelAll(ctx context.Context, symbol string) ([]models.ExchangeOrderResult, error) {
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

type okxInstrumentRaw struct {
	InstID string `json:"instId"`
	State  string `json:"state"`
}

func (c *OkxClient) ValidateSymbol(ctx context.Context, symbol string) (bool, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return false, nil
	}

	var instruments []okxInstrumentRaw
	err = c.publicGet(ctx, "/api/v5/public/instruments", "instType=SPOT", &instruments)
	if err != nil {
		return false, err
	}

	for _, i := range instruments {
		if i.InstID == instID && i.State == "live" {
			return true, nil
		}
	}
	return false, nil
}

func (c *OkxClient) DiscoverBtcPairs(ctx context.Context) ([]string, error) {
	return nil, errors.New("discover_btc_pairs not implemented for this exchange")
}

func (c *OkxClient) GetCurrentPrice(ctx context.Context, symbol string) (float64, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return 0.0, err
	}

	type okxTickerShortRaw struct {
		Last string `json:"last"`
	}
	var ticker []okxTickerShortRaw
	err = c.signedGet(ctx, "market", "/api/v5/market/ticker", "instId="+instID, &ticker)
	if err != nil {
		return 0.0, err
	}

	if len(ticker) == 0 {
		return 0.0, fmt.Errorf("no ticker for %s", instID)
	}

	return strconv.ParseFloat(ticker[0].Last, 64)
}

func (c *OkxClient) GetKlines(ctx context.Context, symbol string, interval string, limit uint32) ([]models.Ohlcv, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("instId=%s&bar=%s&limit=%d", instID, interval, limit)
	var raw [][]string
	err = c.signedGet(ctx, "market", "/api/v5/market/candles", query, &raw)
	if err != nil {
		return nil, err
	}

	var klines []models.Ohlcv
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}
		openTime, _ := strconv.ParseInt(row[0], 10, 64)
		open, _ := strconv.ParseFloat(row[1], 64)
		high, _ := strconv.ParseFloat(row[2], 64)
		low, _ := strconv.ParseFloat(row[3], 64)
		closePrice, _ := strconv.ParseFloat(row[4], 64)
		vol, _ := strconv.ParseFloat(row[5], 64)
		
		var quoteVol float64
		if len(row) >= 8 {
			quoteVol, _ = strconv.ParseFloat(row[7], 64)
		}

		klines = append(klines, models.Ohlcv{
			OpenTime:    openTime,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       closePrice,
			Volume:      vol,
			QuoteVolume: quoteVol,
		})
	}
	return klines, nil
}

type okxTickerRaw struct {
	Last      string `json:"last"`
	Open24h   string `json:"open24h"`
	High24h   string `json:"high24h"`
	Low24h    string `json:"low24h"`
	VolCcy24h string `json:"volCcy24h"`
}

type okxBookRaw struct {
	Bids [][]string `json:"bids"`
	Asks [][]string `json:"asks"`
}

func (c *OkxClient) GetMarketData(ctx context.Context, symbol string) (models.BtcMarketData, error) {
	instID, err := c.toOkxInstID(symbol)
	if err != nil {
		return models.BtcMarketData{}, err
	}

	var ticker []okxTickerRaw
	err = c.signedGet(ctx, "market", "/api/v5/market/ticker", "instId="+instID, &ticker)
	if err != nil {
		return models.BtcMarketData{}, err
	}
	if len(ticker) == 0 {
		return models.BtcMarketData{}, fmt.Errorf("no ticker for %s", instID)
	}

	var books []okxBookRaw
	err = c.signedGet(ctx, "market", "/api/v5/market/books", "instId="+instID+"&sz=20", &books)
	if err != nil {
		return models.BtcMarketData{}, err
	}

	var bestBid, bestAsk, bidDepth, askDepth float64
	if len(books) > 0 {
		book := books[0]
		if len(book.Bids) > 0 {
			bestBid, _ = strconv.ParseFloat(book.Bids[0][0], 64)
			for _, b := range book.Bids {
				sz, _ := strconv.ParseFloat(b[1], 64)
				bidDepth += sz
			}
		}
		if len(book.Asks) > 0 {
			bestAsk, _ = strconv.ParseFloat(book.Asks[0][0], 64)
			for _, a := range book.Asks {
				sz, _ := strconv.ParseFloat(a[1], 64)
				askDepth += sz
			}
		}
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

	t := ticker[0]
	last, _ := strconv.ParseFloat(t.Last, 64)
	open24h, _ := strconv.ParseFloat(t.Open24h, 64)
	high24h, _ := strconv.ParseFloat(t.High24h, 64)
	low24h, _ := strconv.ParseFloat(t.Low24h, 64)
	quoteVol, _ := strconv.ParseFloat(t.VolCcy24h, 64)

	tickerChange := 0.0
	if open24h > 0.0 {
		tickerChange = (last - open24h) / open24h * 100.0
	}

	mid := 0.0
	if bestAsk > 0.0 && bestBid > 0.0 {
		mid = (bestAsk + bestBid) / 2.0
	}

	volatilityScore := 5.0
	if high24h > 0.0 && low24h > 0.0 && mid > 0.0 {
		volatilityScore = ((high24h - low24h) / mid * 100.0)
		if volatilityScore > 10.0 {
			volatilityScore = 10.0
		}
		if volatilityScore < 0.0 {
			volatilityScore = 0.0
		}
	}

	tickerVolScore := quoteVol / 50000000.0
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
