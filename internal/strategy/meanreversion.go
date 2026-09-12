package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

type MeanReversion struct {
	Exit
	Period               int
	StdDev               float64
	Oversold, Overbought float64
}

func (m MeanReversion) Name() string {
	return fmt.Sprintf("Mean Reversion BB%d/%.1f", m.Period, m.StdDev)
}

func (m MeanReversion) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < m.Period+2 {
		return hold
	}
	i := len(cs) - 1
	closes := market.Closes(cs)
	upper, mid, lower := indicators.Bollinger(closes, m.Period, m.StdDev)
	rsi := indicators.RSI(closes, rsiPeriod)

	if !ok(upper[i], mid[i], lower[i], rsi[i]) {
		return hold
	}
	price := closes[i]

	if pos != nil {
		reversal := ""
		if !pos.Short && price >= mid[i] {
			reversal = "reverted to Bollinger mid-band"
		}
		if pos.Short && price <= mid[i] {
			reversal = "reverted to Bollinger mid-band"
		}
		if sig, exit := m.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "waiting for mean reversion"}
	}

	if price < lower[i] && rsi[i] < m.Oversold {
		return Signal{Action: Long, Reason: fmt.Sprintf("below lower band %.2f, RSI %.1f oversold", lower[i], rsi[i])}
	}
	if price > upper[i] && rsi[i] > m.Overbought {
		return Signal{Action: Short, Reason: fmt.Sprintf("above upper band %.2f, RSI %.1f overbought", upper[i], rsi[i])}
	}
	return hold
}
