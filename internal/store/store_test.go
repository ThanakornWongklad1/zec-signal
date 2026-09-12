package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSaveSignalReportsOnlyRealChanges(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sig := Signal{Symbol: "ZECUSDT", Strategy: "Trend", Action: "HOLD", Reason: "no setup", Price: 100}
	if changed, err := st.SaveSignal(sig); err != nil || !changed {
		t.Fatalf("first save should count as a change: changed=%v err=%v", changed, err)
	}
	sig.Reason = "still no setup"
	if changed, err := st.SaveSignal(sig); err != nil || changed {
		t.Fatalf("same action should not count as a change: changed=%v err=%v", changed, err)
	}
	sig.Action = "LONG"
	if changed, err := st.SaveSignal(sig); err != nil || !changed {
		t.Fatalf("new action should count as a change: changed=%v err=%v", changed, err)
	}
}

func TestPositionLifecycleAndEquity(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if pos, err := st.CurrentPosition("ZECUSDT"); err != nil || pos != nil {
		t.Fatalf("expected no open position, got %+v err=%v", pos, err)
	}
	if err := st.OpenPosition("ZECUSDT", "Trend", "LONG", 1010.5); err != nil {
		t.Fatal(err)
	}
	pos, err := st.CurrentPosition("ZECUSDT")
	if err != nil || pos == nil || pos.Side != "LONG" || pos.EntryPrice != 1010.5 {
		t.Fatalf("unexpected position %+v err=%v", pos, err)
	}
	if err := st.ClosePosition("ZECUSDT"); err != nil {
		t.Fatal(err)
	}
	if pos, err := st.CurrentPosition("ZECUSDT"); err != nil || pos != nil {
		t.Fatalf("position should be closed, got %+v err=%v", pos, err)
	}

	if err := st.SetEquity(2500.75); err != nil {
		t.Fatal(err)
	}
	if v, err := st.Equity(); err != nil || v != 2500.75 {
		t.Fatalf("equity = %v err=%v, want 2500.75", v, err)
	}
}

// Two symbols share strategy names, so everything keyed on strategy alone has
// to stay symbol-scoped or one market silently overwrites the other.
func TestSymbolsDoNotCollide(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const name = "Trend EMA21/55"
	if _, err := st.SaveSignal(Signal{Symbol: "ZECUSDT", Strategy: name, Action: "LONG"}); err != nil {
		t.Fatal(err)
	}
	changed, err := st.SaveSignal(Signal{Symbol: "BTCUSDT", Strategy: name, Action: "SHORT"})
	if err != nil || !changed {
		t.Fatalf("first BTC signal should be a change, not a diff against ZEC: changed=%v err=%v", changed, err)
	}
	sigs, err := st.Signals()
	if err != nil {
		t.Fatal(err)
	}
	if sigs["ZECUSDT"][name].Action != "LONG" || sigs["BTCUSDT"][name].Action != "SHORT" {
		t.Fatalf("signals collided across symbols: %+v", sigs)
	}

	if err := st.OpenPosition("ZECUSDT", name, "LONG", 50); err != nil {
		t.Fatal(err)
	}
	if err := st.OpenPosition("BTCUSDT", name, "SHORT", 90000); err != nil {
		t.Fatal(err)
	}
	if err := st.ClosePosition("ZECUSDT"); err != nil {
		t.Fatal(err)
	}
	pos, err := st.CurrentPosition("BTCUSDT")
	if err != nil || pos == nil || pos.EntryPrice != 90000 {
		t.Fatalf("closing ZEC must not close BTC, got %+v err=%v", pos, err)
	}

	if err := st.SetLastPrice("ZECUSDT", 50); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLastPrice("BTCUSDT", 90000); err != nil {
		t.Fatal(err)
	}
	if v, err := st.LastPrice("ZECUSDT"); err != nil || v != 50 {
		t.Fatalf("ZEC reference price = %v err=%v, want 50", v, err)
	}
}

func TestMigrateFromSingleSymbolSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
		CREATE TABLE backtest_results (strategy TEXT PRIMARY KEY, trades INTEGER NOT NULL,
		  win_rate REAL NOT NULL, profit_factor REAL NOT NULL, max_drawdown REAL NOT NULL,
		  net_profit REAL NOT NULL, passed INTEGER NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE signals (strategy TEXT PRIMARY KEY, action TEXT NOT NULL, reason TEXT NOT NULL,
		  price REAL NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE positions (id INTEGER PRIMARY KEY AUTOINCREMENT, strategy TEXT NOT NULL,
		  side TEXT NOT NULL, entry_price REAL NOT NULL, opened_at TEXT NOT NULL, closed_at TEXT);
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO backtest_results VALUES ('Donchian96 Breakout', 902, 0.3, 1.07, 0.402, 1131, 0, '2026-01-01T00:00:00Z');
		INSERT INTO signals VALUES ('Donchian96 Breakout', 'LONG', 'broke out', 41.5, '2026-01-01T00:00:00Z');
		INSERT INTO positions (strategy, side, entry_price, opened_at) VALUES ('Donchian96 Breakout', 'LONG', 41.5, '2026-01-01T00:00:00Z');
		INSERT INTO settings VALUES ('last_price', '41.5');`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	defer st.Close()

	bts, err := st.Backtests()
	if err != nil || len(bts) != 1 || bts[0].Symbol != "ZECUSDT" || bts[0].Trades != 902 {
		t.Fatalf("backtest rows lost their numbers or symbol: %+v err=%v", bts, err)
	}
	sigs, err := st.Signals()
	if err != nil || sigs["ZECUSDT"]["Donchian96 Breakout"].Action != "LONG" {
		t.Fatalf("signal did not migrate to ZECUSDT: %+v err=%v", sigs, err)
	}
	pos, err := st.CurrentPosition("ZECUSDT")
	if err != nil || pos == nil || pos.EntryPrice != 41.5 {
		t.Fatalf("open position did not migrate: %+v err=%v", pos, err)
	}
	if v, err := st.LastPrice("ZECUSDT"); err != nil || v != 41.5 {
		t.Fatalf("reference price did not migrate: %v err=%v", v, err)
	}
}
