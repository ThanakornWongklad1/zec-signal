package strategy

import (
	"fmt"
	"math"
	"time"

	"zec-signal/internal/market"
)

type Action string

const (
	Long  Action = "LONG"
	Short Action = "SHORT"
	Hold  Action = "HOLD"
	Close Action = "CLOSE"
)

// Window is how many trailing candles a strategy needs; the backtest feeds
// exactly this many so replaying history stays linear instead of quadratic.
const Window = 150

const (
	VolumePeriod = 20
	rsiPeriod    = 14
)

// Exit carries the exit rules every strategy obeys identically. Held as fields
// rather than package consts so a parameter sweep can vary them per candidate.
type Exit struct {
	Stop float64       // adverse *price* move that closes the position
	Hold time.Duration // hard time stop, sized to the funding window
}

func (e Exit) StopPct() float64 { return e.Stop }

type Signal struct {
	Action  Action
	Reason  string
	StopHit bool
}

type Open struct {
	Short     bool
	Entry     float64
	EntryTime time.Time
}

type Strategy interface {
	Name() string
	StopPct() float64
	Evaluate(cs []market.Candle, pos *Open) Signal
}

// tuned holds the best parameter set the sweep in internal/backtest/sweep_test.go
// found for each strategy, per symbol, against that symbol's real 15m history.
// Parameters are not transferable between symbols — each set was swept against
// its own volatility and price-scale regime. Any Slow or Period here must stay
// under Window, or Evaluate short-circuits to hold and the strategy silently
// takes zero trades.
//
// The first four were selected against the full 3y before walk-forward existed,
// so their training-window numbers are contaminated and only their holdout
// column says anything. The last three were selected on the training window
// alone; for those the holdout is a real out-of-sample read. Only two entries
// across both symbols clear the gate on both windows: ZEC MeanReversion and BTC
// RegimeTrend. Everything else stays applied, flagged FAIL, so the dashboard
// reports real numbers rather than hiding the misses.
var tuned = map[string][]Strategy{
	"ZECUSDT": {
		Trend{Exit: Exit{Stop: 0.05, Hold: 48 * time.Hour}, Fast: 34, Slow: 89, RSILow: 45, RSIHigh: 65},
		MeanReversion{Exit: Exit{Stop: 0.10, Hold: 8 * time.Hour}, Period: 20, StdDev: 3.5, Oversold: 20, Overbought: 80},
		Breakout{Exit: Exit{Stop: 0.07, Hold: 48 * time.Hour}, Period: 96},
		Momentum{Exit: Exit{Stop: 0.07, Hold: 8 * time.Hour}, Fast: 34, Slow: 72, Signal: 18},
		// Best on the training window; misses the gate there on profit factor
		// (1.46 against 1.5) and misses the holdout the same way (1.44). A
		// near-miss that behaves the same on both windows is at least honest —
		// unlike the VWAP entry below, nothing here is being memorised.
		RegimeTrend{Trend: Trend{Exit: Exit{Stop: 0.05, Hold: 8 * time.Hour}, Fast: 55, Slow: 144, RSILow: 40, RSIHigh: 70}, ADXPeriod: 14, ADXMin: 20},
		// Clears the training window outright (profit factor 2.39) and then
		// loses money on the holdout (0.78, expectancy -0.108R). So did every
		// other candidate in the training top ten — all ten, both symbols. Kept
		// applied and flagged FAIL as the worked example of memorisation.
		VWAPDeviation{Exit: Exit{Stop: 0.10, Hold: 48 * time.Hour}, Period: 144, Deviation: 0.10, Oversold: 30, Overbought: 70},
		// Loses money on both windows. Squeeze release has no edge here: the
		// ranking prefers the *widest* threshold swept, i.e. the compression
		// precondition contributes nothing and this degenerates to a plain band
		// breakout.
		Squeeze{Exit: Exit{Stop: 0.10, Hold: 8 * time.Hour}, Period: 50, StdDev: 3.0, MaxWidth: 0.035},
	},
	"BTCUSDT": {
		Trend{Exit: Exit{Stop: 0.10, Hold: 168 * time.Hour}, Fast: 34, Slow: 120, RSILow: 40, RSIHigh: 70},
		MeanReversion{Exit: Exit{Stop: 0.10, Hold: 8 * time.Hour}, Period: 30, StdDev: 3.0, Oversold: 15, Overbought: 85},
		Breakout{Exit: Exit{Stop: 0.03, Hold: 16 * time.Hour}, Period: 120},
		Momentum{Exit: Exit{Stop: 0.10, Hold: 168 * time.Hour}, Fast: 34, Slow: 72, Signal: 18},
		// The one genuinely validated addition: clears the gate on the training
		// window (98 trades, profit factor 1.74) and again on the holdout it
		// never saw (48 trades, 1.89), with expectancy rising rather than
		// decaying. Wins under a third of the time — which is exactly the shape
		// the old win-rate gate would have thrown away.
		RegimeTrend{Trend: Trend{Exit: Exit{Stop: 0.05, Hold: 168 * time.Hour}, Fast: 34, Slow: 120, RSILow: 30, RSIHigh: 70}, ADXPeriod: 14, ADXMin: 20},
		// Same collapse as ZEC's: training profit factor 1.86, holdout 0.28 on
		// 15 trades. Period 144 is the ceiling Window allows, so a longer anchor
		// is untested rather than rejected — but nothing suggests it would help.
		VWAPDeviation{Exit: Exit{Stop: 0.03, Hold: 168 * time.Hour}, Period: 144, Deviation: 0.05, Oversold: 30, Overbought: 70},
		// Loses money on both windows, same as on ZEC. Note the 43% training
		// drawdown: this is the best of a uniformly bad grid, not a near miss.
		Squeeze{Exit: Exit{Stop: 0.015, Hold: 48 * time.Hour}, Period: 50, StdDev: 3.0, MaxWidth: 0.025},
	},
}

// For returns the tuned strategies for a symbol, or nil for an unswept one.
func For(symbol string) []Strategy { return tuned[symbol] }

var hold = Signal{Action: Hold, Reason: "no setup"}

// sharedExit applies the exit rules every strategy must obey identically —
// hard stop, then the funding-window force-close, then the strategy's own
// reversal condition. Kept in one place so the four strategies cannot drift.
func (e Exit) sharedExit(pos *Open, c market.Candle, reversal string) (Signal, bool) {
	stop := pos.Entry * (1 - e.Stop)
	breached := c.Low <= stop
	if pos.Short {
		stop = pos.Entry * (1 + e.Stop)
		breached = c.High >= stop
	}
	if breached {
		return Signal{
			Action:  Close,
			Reason:  fmt.Sprintf("stop-loss hit at %.2f", stop),
			StopHit: true,
		}, true
	}
	if !c.OpenTime.Before(pos.EntryTime.Add(e.Hold)) {
		return Signal{Action: Close, Reason: fmt.Sprintf("%.0fh force-close (funding)", e.Hold.Hours())}, true
	}
	if reversal != "" {
		return Signal{Action: Close, Reason: reversal}, true
	}
	return Signal{}, false
}

func ok(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) {
			return false
		}
	}
	return true
}
