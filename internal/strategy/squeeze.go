package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

// Squeeze demands compression before expansion: Donchian fires on every poke
// through a channel, most of which is noise, whereas this only fires when
// Bollinger width had collapsed below MaxWidth first. Far fewer signals, and
// each one follows a build-up of stored energy rather than a random push.
type Squeeze struct {
	Exit
	Period   int
	StdDev   float64
	MaxWidth float64 // (upper-lower)/mid at or below this counts as compressed
}

func (s Squeeze) Name() string {
	return fmt.Sprintf("Squeeze BB%d/%.1f w<%.3f", s.Period, s.StdDev, s.MaxWidth)
}

func (s Squeeze) Evaluate(cs []market.Candle, pos *Open) Signal {
	if len(cs) < s.Period+2 {
		return hold
	}
	i := len(cs) - 1
	closes := market.Closes(cs)
	upper, mid, lower := indicators.Bollinger(closes, s.Period, s.StdDev)
	width := indicators.BBWidth(closes, s.Period, s.StdDev)

	// Bands as of the previous candle: the release candle's own range widens
	// the current band enough to swallow its own breakout otherwise.
	if !ok(upper[i-1], mid[i-1], lower[i-1], width[i-1], mid[i]) {
		return hold
	}
	price := closes[i]

	if pos != nil {
		reversal := ""
		if !pos.Short && price < mid[i] {
			reversal = "expansion failed, back below mid-band"
		}
		if pos.Short && price > mid[i] {
			reversal = "expansion failed, back above mid-band"
		}
		if sig, exit := s.sharedExit(pos, cs[i], reversal); exit {
			return sig
		}
		return Signal{Action: Hold, Reason: "expansion running"}
	}

	if width[i-1] > s.MaxWidth {
		return Signal{Action: Hold, Reason: fmt.Sprintf("no squeeze: band width %.3f", width[i-1])}
	}
	if price > upper[i-1] {
		return Signal{Action: Long, Reason: fmt.Sprintf("squeeze %.3f released up through %.2f", width[i-1], upper[i-1])}
	}
	if price < lower[i-1] {
		return Signal{Action: Short, Reason: fmt.Sprintf("squeeze %.3f released down through %.2f", width[i-1], lower[i-1])}
	}
	return hold
}
