package strategy

import (
	"testing"
	"time"

	"zec-signal/internal/market"
)

// Pinned so these exit assertions stay valid independent of tuned defaults.
var testExit = Exit{Stop: 0.02, Hold: 16 * time.Hour}

func TestSharedExitStopLossLong(t *testing.T) {
	pos := &Open{Entry: 100, EntryTime: time.Unix(0, 0).UTC()}
	c := market.Candle{OpenTime: time.Unix(0, 0).UTC(), Low: 98, High: 101, Close: 99}
	sig, exit := testExit.sharedExit(pos, c, "")
	if !exit || sig.Action != Close || !sig.StopHit {
		t.Fatalf("expected stop-loss close, got %+v exit=%v", sig, exit)
	}
}

func TestSharedExitStopLossShort(t *testing.T) {
	pos := &Open{Short: true, Entry: 100, EntryTime: time.Unix(0, 0).UTC()}
	if _, exit := testExit.sharedExit(pos, market.Candle{High: 101.9, Low: 99}, ""); exit {
		t.Fatal("101.9 is inside the 2% short stop, should not exit")
	}
	sig, exit := testExit.sharedExit(pos, market.Candle{High: 102, Low: 99}, "")
	if !exit || !sig.StopHit {
		t.Fatalf("expected stop-loss close at 102, got %+v exit=%v", sig, exit)
	}
}

func TestSharedExitTimeBeatsReversal(t *testing.T) {
	entry := time.Unix(0, 0).UTC()
	pos := &Open{Entry: 100, EntryTime: entry}
	c := market.Candle{OpenTime: entry.Add(testExit.Hold), Low: 99.5, High: 100.5, Close: 100}
	sig, exit := testExit.sharedExit(pos, c, "some reversal")
	if !exit || sig.Reason != "16h force-close (funding)" {
		t.Fatalf("expected 16h force-close, got %+v exit=%v", sig, exit)
	}
}

func TestSharedExitReversalAndHold(t *testing.T) {
	entry := time.Unix(0, 0).UTC()
	pos := &Open{Entry: 100, EntryTime: entry}
	c := market.Candle{OpenTime: entry.Add(time.Hour), Low: 99.5, High: 100.5, Close: 100}

	if _, exit := testExit.sharedExit(pos, c, ""); exit {
		t.Fatal("no rule triggered, should hold")
	}
	sig, exit := testExit.sharedExit(pos, c, "EMA crossed back down")
	if !exit || sig.Action != Close || sig.StopHit {
		t.Fatalf("expected plain reversal close, got %+v exit=%v", sig, exit)
	}
}
