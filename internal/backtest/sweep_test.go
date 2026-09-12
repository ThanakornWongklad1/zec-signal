package backtest

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"zec-signal/internal/binance"
	"zec-signal/internal/market"
	"zec-signal/internal/risk"
	"zec-signal/internal/strategy"
)

// Tuning aid: the 3y candle history takes real time to paginate out of
// Binance, so it is cached on disk per symbol and interval and every sweep
// replays the same bytes. Guarded by ZEC_SWEEP so `go test ./...` stays fast
// and offline.
func sweepCandles(t *testing.T, symbol, interval string) []market.Candle {
	t.Helper()
	if os.Getenv("ZEC_SWEEP") == "" {
		t.Skip("set ZEC_SWEEP=1 to run tuning sweeps")
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-3y.gob", strings.ToLower(symbol), interval))
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		var cs []market.Candle
		if err := gob.NewDecoder(f).Decode(&cs); err != nil {
			t.Fatalf("decode cache %s: %v", path, err)
		}
		t.Logf("cache hit: %d candles from %s", len(cs), path)
		return cs
	}
	cs, err := binance.New().SpotHistory(symbol, interval, time.Now().AddDate(-3, 0, 0))
	if err != nil {
		t.Fatalf("fetch history: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gob.NewEncoder(f).Encode(cs); err != nil {
		t.Fatal(err)
	}
	t.Logf("cached %d candles to %s", len(cs), path)
	return cs
}

func line(label string, r Result) string {
	return fmt.Sprintf("%-30s trades=%-5d wr=%.1f%% pf=%.2f exp=%.3fR maxDD=%.1f%% net=%.0f pass=%v",
		label, r.Trades, r.WinRate*100, r.ProfitFactor, r.Expectancy, r.MaxDrawdown*100, r.NetProfit, r.Passed)
}

// sweepSymbols are the symbols every 15m grid is run against. Each gets its own
// subtest, so -run 'TestSweepTrend/BTCUSDT' narrows a sweep to one symbol.
var sweepSymbols = []string{"ZECUSDT", "BTCUSDT"}

func TestBaseline(t *testing.T) {
	t.Logf("leverage=%v stopPct=%v", risk.Leverage, risk.StopLossPct)
	for _, symbol := range sweepSymbols {
		t.Run(symbol, func(t *testing.T) {
			for _, r := range RunAll(symbol, sweepCandles(t, symbol, "15m")) {
				t.Log(line(r.Strategy, r))
			}
		})
	}
}

// TestWalkForward scores the parameters actually applied in strategy.For on
// the two windows separately. The existing four were tuned on full history, so
// their train numbers are contaminated and only the holdout column means
// anything for them; the new three were selected on train alone, so for those
// the holdout is a genuine out-of-sample read.
func TestWalkForward(t *testing.T) {
	for _, symbol := range sweepSymbols {
		t.Run(symbol, func(t *testing.T) {
			train, holdout := Split(sweepCandles(t, symbol, "15m"))
			for _, s := range strategy.For(symbol) {
				t.Log("TRAIN   " + line(s.Name(), Run(s, train)))
				t.Log("HOLDOUT " + line(s.Name(), Run(s, holdout)))
			}
		})
	}
}

type cand struct {
	label string
	s     strategy.Strategy
}

// exits is the shared-exit half of every grid: stop distance also sets position
// size (notional = equity*RiskPct/stop), so it moves friction as well as hit rate.
func exits() []strategy.Exit {
	var out []strategy.Exit
	for _, stop := range []float64{0.015, 0.02, 0.03, 0.05, 0.07, 0.10} {
		for _, hold := range []time.Duration{8 * time.Hour, 16 * time.Hour, 48 * time.Hour, 96 * time.Hour, 168 * time.Hour} {
			out = append(out, strategy.Exit{Stop: stop, Hold: hold})
		}
	}
	return out
}

func label(e strategy.Exit, s strategy.Strategy) string {
	return fmt.Sprintf("%s stop=%.3f hold=%.0fh", s.Name(), e.Stop, e.Hold.Hours())
}

// minTrades keeps single-lucky-streak parameter sets out of the ranking.
const minTrades = 50

// sweep ranks candidates on the training window ONLY — the holdout must stay
// unseen by selection or it stops being a holdout. It is scored afterwards,
// for the survivors alone, and printed alongside so a set that only works
// in-sample is visible as such at the moment you pick it.
func sweep(t *testing.T, cs []market.Candle, cands []cand) {
	t.Helper()
	train, holdout := Split(cs)
	cs = train
	res := make([]Result, len(cands))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c cand) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := Run(c.s, cs)
			r.Log = nil // 900 candidates x thousands of trades is a lot of retained memory
			res[i] = r
		}(i, c)
	}
	wg.Wait()

	idx := make([]int, 0, len(res))
	for i, r := range res {
		if r.Trades >= minTrades {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool {
		ra, rb := res[idx[a]], res[idx[b]]
		if ra.Passed != rb.Passed {
			return ra.Passed
		}
		return ra.ProfitFactor > rb.ProfitFactor
	})
	t.Logf("%d candidates, %d with >=%d trades (train window)", len(res), len(idx), minTrades)
	for n, i := range idx {
		if n == 10 {
			break
		}
		t.Log("TRAIN   " + line(cands[i].label, res[i]))
		t.Log("HOLDOUT " + line(cands[i].label, Run(cands[i].s, holdout)))
	}
}

func trendCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, p := range [][2]int{{9, 21}, {13, 34}, {21, 55}, {21, 89}, {34, 89}, {34, 120}, {55, 144}} {
			for _, b := range [][2]float64{{30, 70}, {40, 70}, {45, 65}, {50, 60}} {
				s := strategy.Trend{Exit: e, Fast: p[0], Slow: p[1], RSILow: b[0], RSIHigh: b[1]}
				cands = append(cands, cand{fmt.Sprintf("%s rsi=%.0f/%.0f", label(e, s), b[0], b[1]), s})
			}
		}
	}
	return cands
}

func meanReversionCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, period := range []int{20, 30, 50} {
			for _, sd := range []float64{2.0, 2.5, 3.0, 3.5} {
				for _, b := range [][2]float64{{30, 70}, {20, 80}, {15, 85}, {10, 90}} {
					s := strategy.MeanReversion{Exit: e, Period: period, StdDev: sd, Oversold: b[0], Overbought: b[1]}
					cands = append(cands, cand{fmt.Sprintf("%s rsi=%.0f/%.0f", label(e, s), b[0], b[1]), s})
				}
			}
		}
	}
	return cands
}

func breakoutCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, period := range []int{20, 40, 60, 96, 120} {
			s := strategy.Breakout{Exit: e, Period: period}
			cands = append(cands, cand{label(e, s), s})
		}
	}
	return cands
}

func momentumCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, p := range [][3]int{{12, 26, 9}, {19, 39, 9}, {24, 52, 18}, {34, 72, 18}} {
			s := strategy.Momentum{Exit: e, Fast: p[0], Slow: p[1], Signal: p[2]}
			cands = append(cands, cand{label(e, s), s})
		}
	}
	return cands
}

// regimeTrendCands deliberately contains the EMA/RSI pairs already tuned for
// each symbol, so the ADX-filtered result can be read against the unfiltered
// Trend baseline at identical settings and the filter's own effect isolated.
func regimeTrendCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, p := range [][2]int{{21, 55}, {34, 89}, {34, 120}, {55, 144}} {
			for _, b := range [][2]float64{{30, 70}, {40, 70}, {45, 65}} {
				for _, adx := range []float64{20, 25, 30} {
					s := strategy.RegimeTrend{
						Trend:     strategy.Trend{Exit: e, Fast: p[0], Slow: p[1], RSILow: b[0], RSIHigh: b[1]},
						ADXPeriod: 14, ADXMin: adx,
					}
					cands = append(cands, cand{fmt.Sprintf("%s rsi=%.0f/%.0f", label(e, s), b[0], b[1]), s})
				}
			}
		}
	}
	return cands
}

// VWAP windows stop at 144 bars (36h): strategy.Window is 150, so anything
// longer never warms up inside the slice Evaluate is handed and the strategy
// would silently take zero trades.
func vwapCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, period := range []int{32, 64, 96, 144} {
			for _, dev := range []float64{0.02, 0.03, 0.05, 0.07, 0.10} {
				for _, b := range [][2]float64{{30, 70}, {20, 80}, {15, 85}} {
					s := strategy.VWAPDeviation{Exit: e, Period: period, Deviation: dev, Oversold: b[0], Overbought: b[1]}
					cands = append(cands, cand{fmt.Sprintf("%s rsi=%.0f/%.0f", label(e, s), b[0], b[1]), s})
				}
			}
		}
	}
	return cands
}

// Band width is absolute, and BTC's runs roughly a third of ZEC's, so the
// threshold grid spans both: 0.005 is a deep BTC squeeze, 0.035 a mild ZEC one.
func squeezeCands() []cand {
	var cands []cand
	for _, e := range exits() {
		for _, period := range []int{20, 30, 50} {
			for _, sd := range []float64{2.0, 2.5, 3.0} {
				for _, w := range []float64{0.003, 0.005, 0.008, 0.012, 0.018, 0.025, 0.035} {
					s := strategy.Squeeze{Exit: e, Period: period, StdDev: sd, MaxWidth: w}
					cands = append(cands, cand{label(e, s), s})
				}
			}
		}
	}
	return cands
}

func sweepAll(t *testing.T, cands []cand) {
	t.Helper()
	for _, symbol := range sweepSymbols {
		t.Run(symbol, func(t *testing.T) { sweep(t, sweepCandles(t, symbol, "15m"), cands) })
	}
}

func TestSweepTrend(t *testing.T)         { sweepAll(t, trendCands()) }
func TestSweepMeanReversion(t *testing.T) { sweepAll(t, meanReversionCands()) }
func TestSweepBreakout(t *testing.T)      { sweepAll(t, breakoutCands()) }
func TestSweepMomentum(t *testing.T)      { sweepAll(t, momentumCands()) }
func TestSweepRegimeTrend(t *testing.T)   { sweepAll(t, regimeTrendCands()) }

// TestADXFilterVsBare answers the question RegimeTrend exists to ask, which a
// grid sweep cannot: it runs matched pairs — identical Trend settings with and
// without the ADX gate — so any difference in the numbers is the regime filter
// and nothing else. Logs only; the verdict is a judgement call, not an assert.
func TestADXFilterVsBare(t *testing.T) {
	pairs := []struct {
		symbol string
		e      strategy.Exit
		fast   int
		slow   int
		lo, hi float64
	}{
		{"ZECUSDT", strategy.Exit{Stop: 0.05, Hold: 8 * time.Hour}, 55, 144, 40, 70}, // RegimeTrend sweep winner
		{"ZECUSDT", strategy.Exit{Stop: 0.05, Hold: 48 * time.Hour}, 34, 89, 45, 65}, // tuned bare Trend
		{"BTCUSDT", strategy.Exit{Stop: 0.03, Hold: 168 * time.Hour}, 34, 120, 45, 65},
		{"BTCUSDT", strategy.Exit{Stop: 0.10, Hold: 168 * time.Hour}, 34, 120, 40, 70},
	}
	cache := map[string][]market.Candle{}
	for _, p := range pairs {
		cs, seen := cache[p.symbol]
		if !seen {
			cs = sweepCandles(t, p.symbol, "15m")
			cache[p.symbol] = cs
		}
		bare := strategy.Trend{Exit: p.e, Fast: p.fast, Slow: p.slow, RSILow: p.lo, RSIHigh: p.hi}
		t.Log(p.symbol + "  BARE   " + line(label(p.e, bare), Run(bare, cs)))
		for _, adx := range []float64{20, 25, 30} {
			f := strategy.RegimeTrend{Trend: bare, ADXPeriod: 14, ADXMin: adx}
			t.Log(p.symbol + "  FILTER " + line(label(p.e, f), Run(f, cs)))
		}
	}
}
func TestSweepVWAP(t *testing.T)    { sweepAll(t, vwapCands()) }
func TestSweepSqueeze(t *testing.T) { sweepAll(t, squeezeCands()) }

// 5m variants: same grids, same shared-exit assumptions, different candle
// interval — answers whether shorter candles trade at all profitably here,
// or whether friction (fixed per-trade, same across intervals) just eats a
// bigger share of ZEC's smaller 5m moves. See exits()/backtest.SlippagePct.
func TestSweepTrend5m(t *testing.T)    { sweep(t, sweepCandles(t, "ZECUSDT", "5m"), trendCands()) }
func TestSweepBreakout5m(t *testing.T) { sweep(t, sweepCandles(t, "ZECUSDT", "5m"), breakoutCands()) }
func TestSweepMomentum5m(t *testing.T) { sweep(t, sweepCandles(t, "ZECUSDT", "5m"), momentumCands()) }
func TestSweepMeanReversion5m(t *testing.T) {
	sweep(t, sweepCandles(t, "ZECUSDT", "5m"), meanReversionCands())
}
