package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

type Trend struct {
	Exit
	Fast, Slow      int
	RSILow, RSIHigh float64
}

func (t Trend) Name() string { return fmt.Sprintf("Trend EMA%d/%d", t.Fast, t.Slow) }

func (t Trend) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < t.Slow+2 {
		return hold
	}
	i := len(cs) - 1
	closes := market.Closes(cs)
	fast := indicators.EMA(closes, t.Fast)
	slow := indicators.EMA(closes, t.Slow)
	rsi := indicators.RSI(closes, rsiPeriod)
	vol := indicators.SMA(market.Volumes(cs), VolumePeriod)

	if !ok(fast[i], slow[i], fast[i-1], slow[i-1], rsi[i], vol[i]) {
		return hold
	}
	crossUp := fast[i-1] <= slow[i-1] && fast[i] > slow[i]
	crossDown := fast[i-1] >= slow[i-1] && fast[i] < slow[i]

	if pos != nil {
		reversal := ""
		if pos.Short && crossUp {
			reversal = "EMA crossed back up"
		}
		if !pos.Short && crossDown {
			reversal = "EMA crossed back down"
		}
		if sig, exit := t.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "trend intact"}
	}

	if cs[i].Volume <= vol[i] {
		return Signal{Action: Hold, Reason: "volume below 20-period average"}
	}
	if crossUp && rsi[i] >= t.RSILow && rsi[i] <= t.RSIHigh {
		return Signal{Action: Long, Reason: fmt.Sprintf("EMA%d>EMA%d cross, RSI %.1f, volume confirmed", t.Fast, t.Slow, rsi[i])}
	}
	if crossDown && rsi[i] <= 100-t.RSILow && rsi[i] >= 100-t.RSIHigh {
		return Signal{Action: Short, Reason: fmt.Sprintf("EMA%d<EMA%d cross, RSI %.1f, volume confirmed", t.Fast, t.Slow, rsi[i])}
	}
	return hold
}
