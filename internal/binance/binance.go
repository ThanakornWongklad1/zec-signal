package binance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"zec-signal/internal/market"
)

const (
	spotBase    = "https://api.binance.com/api/v3/klines"
	futuresBase = "https://fapi.binance.com/fapi/v1/klines"
	tickerBase  = "https://fapi.binance.com/fapi/v1/ticker/price"
	maxLimit    = 1000
)

type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}}
}

// SpotHistory pages forward from start until no further candles are returned.
func (c *Client) SpotHistory(symbol, interval string, start time.Time) ([]market.Candle, error) {
	var all []market.Candle
	from := start.UnixMilli()
	for {
		batch, err := c.klines(spotBase, symbol, interval, from, maxLimit)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		next := batch[len(batch)-1].OpenTime.UnixMilli() + 1
		if next <= from {
			break
		}
		from = next
		if len(batch) < maxLimit {
			break
		}
	}
	return all, nil
}

func (c *Client) FuturesKlines(symbol, interval string, limit int) ([]market.Candle, error) {
	return c.klines(futuresBase, symbol, interval, 0, limit)
}

// Price is a lightweight last-trade lookup, for fast polling — no candle
// aggregation, one float, cheap enough to call far more often than klines.
func (c *Client) Price(symbol string) (float64, error) {
	resp, err := c.http.Get(tickerBase + "?symbol=" + symbol)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance %s: status %d", tickerBase, resp.StatusCode)
	}
	var out struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decode ticker: %w", err)
	}
	return strconv.ParseFloat(out.Price, 64)
}

func (c *Client) klines(base, symbol, interval string, startMS int64, limit int) ([]market.Candle, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	q.Set("limit", strconv.Itoa(limit))
	if startMS > 0 {
		q.Set("startTime", strconv.FormatInt(startMS, 10))
	}

	resp, err := c.http.Get(base + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance %s: status %d", base, resp.StatusCode)
	}

	var raw [][]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode klines: %w", err)
	}

	out := make([]market.Candle, 0, len(raw))
	for _, r := range raw {
		if len(r) < 6 {
			return nil, fmt.Errorf("unexpected kline shape: %d fields", len(r))
		}
		var openMS int64
		if err := json.Unmarshal(r[0], &openMS); err != nil {
			return nil, err
		}
		vals := make([]float64, 5)
		for i := range vals {
			var s string
			if err := json.Unmarshal(r[i+1], &s); err != nil {
				return nil, err
			}
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		out = append(out, market.Candle{
			OpenTime: time.UnixMilli(openMS).UTC(),
			Open:     vals[0],
			High:     vals[1],
			Low:      vals[2],
			Close:    vals[3],
			Volume:   vals[4],
		})
	}
	return out, nil
}
