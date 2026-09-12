package backtest

import (
	"math"
	"time"

	"zec-signal/internal/market"
	"zec-signal/internal/risk"
	"zec-signal/internal/strategy"
)

// ZEC perps have a thinner book than BTC/ETH, so fills are modelled 10bps
// adverse to the signal price rather than assuming the close is obtainable.
// BTC is charged the same 10bps, which is pessimistic for it — a deliberate
// floor, so a BTC parameter set that clears the gate here clears it for real.
const (
	SlippagePct = 0.0010
	TakerFeePct = 0.0004
)

const (
	GateProfitFactor = 1.5
	GateMaxDrawdown  = 0.25

	// GateExpectancy is a floor on mean R — net profit divided by the total
	// amount risked across all trades, so it reads as "profit per unit of risk
	// taken" and is comparable across symbols and price scales. It replaces a
	// win-rate floor, which rejected trend-following outright: winning a third
	// of the time with large winners is a sound shape, not a broken one.
	//
	// 0.05 is set off friction rather than picked round: a round trip costs
	// 2*(SlippagePct+TakerFeePct) = 28bps of notional, which at a 5% stop is
	// 0.056R. So the floor is roughly one round trip's worth of edge — clear of
	// fee noise, and not so high it only admits outliers.
	GateExpectancy = 0.05
)

const StartEquity = 10000.0

type Trade struct {
	Short     bool
	Entry     float64
	Exit      float64
	Qty       float64
	EntryTime time.Time
	ExitTime  time.Time
	PnL       float64
	Reason    string
}

type Result struct {
	Strategy     string
	Trades       int
	Wins         int
	WinRate      float64
	ProfitFactor float64
	MaxDrawdown  float64
	NetProfit    float64
	FinalEquity  float64
	Expectancy   float64 // mean R: net profit per unit of risk staked
	Passed       bool
	Log          []Trade
}

func Run(s strategy.Strategy, cs []market.Candle) Result {
	res := Result{Strategy: s.Name(), FinalEquity: StartEquity}
	if len(cs) <= strategy.Window {
		return res
	}

	stopPct := s.StopPct()
	equity, peak := StartEquity, StartEquity
	var grossWin, grossLoss float64
	var pos *strategy.Open
	var qty, entryFees, risked float64

	for i := strategy.Window - 1; i < len(cs); i++ {
		window := cs[i-strategy.Window+1 : i+1]
		c := cs[i]
		sig := s.Evaluate(window, pos)

		if pos == nil {
			if sig.Action != strategy.Long && sig.Action != strategy.Short {
				continue
			}
			short := sig.Action == strategy.Short
			entry := fill(c.Close, short, true)
			size := risk.Size(equity, entry, stopPct, short)
			if size.Qty <= 0 {
				continue
			}
			qty = size.Qty
			entryFees = size.Notional * TakerFeePct
			risked += size.Notional * stopPct
			pos = &strategy.Open{Short: short, Entry: entry, EntryTime: c.OpenTime}
			continue
		}

		if sig.Action != strategy.Close {
			continue
		}

		exitPrice := c.Close
		if sig.StopHit {
			exitPrice = pos.Entry * (1 - stopPct)
			if pos.Short {
				exitPrice = pos.Entry * (1 + stopPct)
			}
		}
		exitPrice = fill(exitPrice, pos.Short, false)

		gross := qty * (exitPrice - pos.Entry)
		if pos.Short {
			gross = -gross
		}
		pnl := gross - entryFees - qty*exitPrice*TakerFeePct
		equity += pnl

		if pnl > 0 {
			grossWin += pnl
			res.Wins++
		} else {
			grossLoss += -pnl
		}
		res.Trades++
		res.Log = append(res.Log, Trade{
			Short: pos.Short, Entry: pos.Entry, Exit: exitPrice, Qty: qty,
			EntryTime: pos.EntryTime, ExitTime: c.OpenTime, PnL: pnl, Reason: sig.Reason,
		})

		peak = math.Max(peak, equity)
		if dd := (peak - equity) / peak; dd > res.MaxDrawdown {
			res.MaxDrawdown = dd
		}
		pos = nil

		if equity <= 0 {
			break
		}
	}

	res.FinalEquity = equity
	res.NetProfit = equity - StartEquity
	if res.Trades > 0 {
		res.WinRate = float64(res.Wins) / float64(res.Trades)
	}
	switch {
	case grossLoss > 0:
		res.ProfitFactor = grossWin / grossLoss
	case grossWin > 0:
		res.ProfitFactor = math.Inf(1)
	}
	if risked > 0 {
		res.Expectancy = res.NetProfit / risked
	}
	res.Passed = res.Trades > 0 &&
		res.ProfitFactor >= GateProfitFactor &&
		res.Expectancy >= GateExpectancy &&
		res.MaxDrawdown < GateMaxDrawdown
	return res
}

func fill(price float64, short, opening bool) float64 {
	adverseUp := short != opening // buying pays up, selling gets paid down
	if adverseUp {
		return price * (1 + SlippagePct)
	}
	return price * (1 - SlippagePct)
}

// Split cuts history into the window parameters are tuned on and an untouched
// holdout scored once, after selection. Two thirds of 3y is a 2y/1y split.
func Split(cs []market.Candle) (train, holdout []market.Candle) {
	cut := len(cs) * 2 / 3
	return cs[:cut], cs[cut:]
}

// RunAll reports each strategy's metrics over the whole history, but decides
// the gate walk-forward: parameters were chosen against the training window,
// so its own verdict there is in-sample and proves nothing on its own. A PASS
// additionally requires the holdout — data no sweep ever ranked against — to
// clear the gate too, which is what separates an edge from memorisation.
func RunAll(symbol string, cs []market.Candle) []Result {
	train, holdout := Split(cs)
	var out []Result
	for _, s := range strategy.For(symbol) {
		r := Run(s, cs)
		r.Passed = Run(s, train).Passed && Run(s, holdout).Passed
		out = append(out, r)
	}
	return out
}
