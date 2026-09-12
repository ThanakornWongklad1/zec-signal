package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

// VWAPDeviation is mean reversion anchored to where size actually traded
// rather than to a flat average of closes: a stretch away from rolling VWAP is
// a stretch away from the market's own cost basis, which is a stronger reason
// to expect a snap back than distance from an SMA.
type VWAPDeviation struct {
	Exit
	Period               int
	Deviation            float64 // fraction away from VWAP that arms an entry
	Oversold, Overbought float64
}

func (v VWAPDeviation) Name() string {
	return fmt.Sprintf("VWAP%d Deviation %.1f%%", v.Period, v.Deviation*100)
}

func (v VWAPDeviation) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < v.Period+2 {
		return hold
	}
	i := len(cs) - 1
	closes := market.Closes(cs)
	vwap := indicators.VWAP(market.Highs(cs), market.Lows(cs), closes, market.Volumes(cs), v.Period)
	rsi := indicators.RSI(closes, rsiPeriod)

	if !ok(vwap[i], rsi[i]) {
		return hold
	}
	price := closes[i]

	if pos != nil {
		reversal := ""
		if !pos.Short && price >= vwap[i] {
			reversal = "reverted to VWAP"
		}
		if pos.Short && price <= vwap[i] {
			reversal = "reverted to VWAP"
		}
		if sig, exit := v.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "waiting for VWAP reversion"}
	}

	dev := (price - vwap[i]) / vwap[i]
	if dev <= -v.Deviation && rsi[i] < v.Oversold {
		return Signal{Action: Long, Reason: fmt.Sprintf("%.1f%% below VWAP %.2f, RSI %.1f oversold", -dev*100, vwap[i], rsi[i])}
	}
	if dev >= v.Deviation && rsi[i] > v.Overbought {
		return Signal{Action: Short, Reason: fmt.Sprintf("%.1f%% above VWAP %.2f, RSI %.1f overbought", dev*100, vwap[i], rsi[i])}
	}
	return hold
}
