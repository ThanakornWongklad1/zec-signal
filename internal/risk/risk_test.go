package risk

import (
	"math"
	"testing"
)

func eq(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestSizeLong(t *testing.T) {
	// 1% of 1000 = $10 risk over a 2% stop -> $500 notional, 5 ZEC at $100.
	p := Size(1000, 100, 0.02, false)
	eq(t, "notional", p.Notional, 500)
	eq(t, "qty", p.Qty, 5)
	eq(t, "margin", p.Margin, 100)
	eq(t, "maxLoss", p.MaxLoss, 10)
	eq(t, "stop", p.StopPrice, 98)
}

func TestSizeShortTightStop(t *testing.T) {
	// 1% of 5000 = $50 risk over a 0.5% stop -> $10,000 notional, 40 ZEC at $250.
	p := Size(5000, 250, 0.005, true)
	eq(t, "notional", p.Notional, 10000)
	eq(t, "qty", p.Qty, 40)
	eq(t, "margin", p.Margin, 2000)
	eq(t, "maxLoss", p.MaxLoss, 50)
	eq(t, "stop", p.StopPrice, 251.25)
}

func TestSizeCappedByLeverage(t *testing.T) {
	// Risk-derived notional would be $50,000 but 5x on $1000 caps it at $5,000.
	p := Size(1000, 100, 0.0002, false)
	eq(t, "notional", p.Notional, 5000)
	eq(t, "margin", p.Margin, 1000)
	eq(t, "maxLoss", p.MaxLoss, 1)
}

func TestSizeRejectsBadInput(t *testing.T) {
	if p := Size(0, 100, 0.02, false); p.Qty != 0 {
		t.Errorf("zero equity should size nothing, got %+v", p)
	}
	if p := Size(1000, 100, 0, false); p.Qty != 0 {
		t.Errorf("zero stop should size nothing, got %+v", p)
	}
}
