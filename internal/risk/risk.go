package risk

const (
	// Leverage bounds exchange margin, but the risk-derived notional below
	// (RiskPct/StopLossPct = 0.5x equity) is far under the cap, so this value
	// only moves the Margin figure — there is no liquidation model here.
	Leverage = 5.0
	RiskPct  = 0.01

	// StopLossPct is an adverse *price* move, not a fraction of margin: at 5x a
	// 2% price move is already -10% of posted margin, whereas a literal 2%-of-margin
	// stop would be a 0.4% price move, i.e. inside ZEC's spread plus slippage.
	StopLossPct = 0.02
)

type Position struct {
	Qty       float64
	Notional  float64
	Margin    float64
	MaxLoss   float64
	StopPrice float64
}

// Size derives position size so that hitting the stop costs at most RiskPct of
// equity, capped by the margin actually available at Leverage.
func Size(equity, entry, stopPct float64, short bool) Position {
	if equity <= 0 || entry <= 0 || stopPct <= 0 {
		return Position{}
	}
	notional := equity * RiskPct / stopPct
	if max := equity * Leverage; notional > max {
		notional = max
	}
	stop := entry * (1 - stopPct)
	if short {
		stop = entry * (1 + stopPct)
	}
	return Position{
		Qty:       notional / entry,
		Notional:  notional,
		Margin:    notional / Leverage,
		MaxLoss:   notional * stopPct,
		StopPrice: stop,
	}
}
