package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

type Momentum struct {
	Exit
	Fast, Slow, Signal int
}

func (m Momentum) Name() string {
	return fmt.Sprintf("MACD %d/%d/%d Momentum", m.Fast, m.Slow, m.Signal)
}

func (m Momentum) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < m.Slow+m.Signal+2 {
		return hold
	}
	i := len(cs) - 1
	closes := market.Closes(cs)
	_, _, hist := indicators.MACD(closes, m.Fast, m.Slow, m.Signal)
	vol := indicators.SMA(market.Volumes(cs), VolumePeriod)

	if !ok(hist[i], hist[i-1], vol[i]) {
		return hold
	}
	crossUp := hist[i-1] <= 0 && hist[i] > 0
	crossDown := hist[i-1] >= 0 && hist[i] < 0

	if pos != nil {
		reversal := ""
		if !pos.Short && hist[i] < 0 {
			reversal = "MACD histogram turned negative"
		}
		if pos.Short && hist[i] > 0 {
			reversal = "MACD histogram turned positive"
		}
		if sig, exit := m.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "momentum intact"}
	}

	if cs[i].Volume <= vol[i] {
		return Signal{Action: Hold, Reason: "volume below 20-period average"}
	}
	if crossUp {
		return Signal{Action: Long, Reason: fmt.Sprintf("MACD histogram crossed up (%.4f), volume confirmed", hist[i])}
	}
	if crossDown {
		return Signal{Action: Short, Reason: fmt.Sprintf("MACD histogram crossed down (%.4f), volume confirmed", hist[i])}
	}
	return hold
}
