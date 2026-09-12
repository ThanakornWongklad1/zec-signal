package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

type Breakout struct {
	Exit
	Period int
}

func (b Breakout) Name() string { return fmt.Sprintf("Donchian%d Breakout", b.Period) }

func (b Breakout) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < b.Period+2 {
		return hold
	}
	i := len(cs) - 1
	upper, lower := indicators.Donchian(market.Highs(cs), market.Lows(cs), b.Period)

	// Compare against the channel as of the previous candle, otherwise the
	// breakout candle's own high/low is inside the channel and never triggers.
	if !ok(upper[i-1], lower[i-1]) {
		return hold
	}
	price := cs[i].Close
	mid := (upper[i-1] + lower[i-1]) / 2

	if pos != nil {
		reversal := ""
		if !pos.Short && price < mid {
			reversal = "fell back inside channel"
		}
		if pos.Short && price > mid {
			reversal = "rallied back inside channel"
		}
		if sig, exit := b.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "breakout holding"}
	}

	if price > upper[i-1] {
		return Signal{Action: Long, Reason: fmt.Sprintf("broke %d-period high %.2f", b.Period, upper[i-1])}
	}
	if price < lower[i-1] {
		return Signal{Action: Short, Reason: fmt.Sprintf("broke %d-period low %.2f", b.Period, lower[i-1])}
	}
	return hold
}
