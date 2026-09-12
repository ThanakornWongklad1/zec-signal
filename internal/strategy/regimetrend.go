package strategy

import (
	"fmt"

	"zec-signal/internal/indicators"
	"zec-signal/internal/market"
)

// RegimeTrend is plain Trend with an ADX gate on entries only. Trend's
// diagnosed failure is bleeding through sideways stretches, so the fix belongs
// on the entry, not on the exit: once in a position the trade is managed by
// exactly the same rules as Trend, which also makes the two directly
// comparable — any difference in the numbers is the regime filter alone.
type RegimeTrend struct {
	Trend
	ADXPeriod int
	ADXMin    float64
}

func (r RegimeTrend) Name() string {
	return fmt.Sprintf("Regime Trend EMA%d/%d ADX%d>%.0f", r.Fast, r.Slow, r.ADXPeriod, r.ADXMin)
}

func (r RegimeTrend) Evaluate(cs []market.Candle, pos *Open) Signal {
	sig := r.Trend.Evaluate(cs, pos)
	if pos != nil || (sig.Action != Long && sig.Action != Short) {
		return sig
	}
	i := len(cs) - 1
	adx := indicators.ADX(market.Highs(cs), market.Lows(cs), market.Closes(cs), r.ADXPeriod)
	if !ok(adx[i]) || adx[i] < r.ADXMin {
		return Signal{Action: Hold, Reason: fmt.Sprintf("chop: ADX%d below %.0f", r.ADXPeriod, r.ADXMin)}
	}
	return Signal{Action: sig.Action, Reason: fmt.Sprintf("%s, ADX %.1f trending", sig.Reason, adx[i])}
}
