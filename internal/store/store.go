package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS backtest_results (
  symbol        TEXT NOT NULL,
  strategy      TEXT NOT NULL,
  trades        INTEGER NOT NULL,
  win_rate      REAL NOT NULL,
  profit_factor REAL NOT NULL,
  max_drawdown  REAL NOT NULL,
  net_profit    REAL NOT NULL,
  passed        INTEGER NOT NULL,
  updated_at    TEXT NOT NULL,
  PRIMARY KEY (symbol, strategy)
);
CREATE TABLE IF NOT EXISTS signals (
  symbol     TEXT NOT NULL,
  strategy   TEXT NOT NULL,
  action     TEXT NOT NULL,
  reason     TEXT NOT NULL,
  price      REAL NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (symbol, strategy)
);
CREATE TABLE IF NOT EXISTS positions (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  symbol            TEXT NOT NULL,
  strategy          TEXT NOT NULL,
  side              TEXT NOT NULL,
  entry_price       REAL NOT NULL,
  leverage          REAL NOT NULL DEFAULT 1,
  profit_alert_step INTEGER NOT NULL DEFAULT 0,
  loss_alert_step   INTEGER NOT NULL DEFAULT 0,
  opened_at         TEXT NOT NULL,
  closed_at         TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

type Store struct{ db *sql.DB }

type Backtest struct {
	Symbol       string
	Strategy     string
	Trades       int
	WinRate      float64
	ProfitFactor float64
	MaxDrawdown  float64
	NetProfit    float64
	Passed       bool
	UpdatedAt    time.Time
}

type Signal struct {
	Symbol    string
	Strategy  string
	Action    string
	Reason    string
	Price     float64
	UpdatedAt time.Time
}

type Position struct {
	ID              int64
	Symbol          string
	Strategy        string
	Side            string
	EntryPrice      float64
	Leverage        float64
	ProfitAlertStep int
	LossAlertStep   int
	OpenedAt        time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate upgrades a database written before multi-symbol support. Those tables
// key on strategy alone, which collides as soon as a second symbol reuses a
// strategy name, and their rows carry no symbol — every one of them is ZEC's,
// since ZEC was the only symbol that could have written them. On a database
// already carrying symbol columns every step here is a no-op.
func migrate(db *sql.DB) error {
	rebuild := []struct{ table, cols string }{
		{"backtest_results", "strategy, trades, win_rate, profit_factor, max_drawdown, net_profit, passed, updated_at"},
		{"signals", "strategy, action, reason, price, updated_at"},
	}
	for _, r := range rebuild {
		has, err := hasColumn(db, r.table, "symbol")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		// SQLite cannot widen a primary key in place, so the table is renamed
		// aside, recreated from schema, and copied back with the symbol filled in.
		stmts := []string{
			`ALTER TABLE ` + r.table + ` RENAME TO ` + r.table + `_v1`,
			schema,
			`INSERT INTO ` + r.table + ` SELECT 'ZECUSDT', ` + r.cols + ` FROM ` + r.table + `_v1`,
			`DROP TABLE ` + r.table + `_v1`,
		}
		for _, s := range stmts {
			if _, err := db.Exec(s); err != nil {
				return err
			}
		}
	}

	has, err := hasColumn(db, "positions", "symbol")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE positions ADD COLUMN symbol TEXT NOT NULL DEFAULT 'ZECUSDT'`); err != nil {
			return err
		}
	}

	posCols := []string{"leverage REAL NOT NULL DEFAULT 1", "profit_alert_step INTEGER NOT NULL DEFAULT 0", "loss_alert_step INTEGER NOT NULL DEFAULT 0"}
	for _, col := range posCols {
		name := col[:strings.IndexByte(col, ' ')]
		has, err := hasColumn(db, "positions", name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(`ALTER TABLE positions ADD COLUMN ` + col); err != nil {
				return err
			}
		}
	}

	_, err = db.Exec(`UPDATE settings SET key = 'last_price:ZECUSDT' WHERE key = 'last_price'`)
	return err
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n)
	return n > 0, err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveBacktests(rs []Backtest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rs {
		_, err := tx.Exec(`
			INSERT INTO backtest_results
			  (symbol, strategy, trades, win_rate, profit_factor, max_drawdown, net_profit, passed, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(symbol, strategy) DO UPDATE SET
			  trades=excluded.trades, win_rate=excluded.win_rate,
			  profit_factor=excluded.profit_factor, max_drawdown=excluded.max_drawdown,
			  net_profit=excluded.net_profit, passed=excluded.passed,
			  updated_at=excluded.updated_at`,
			r.Symbol, r.Strategy, r.Trades, r.WinRate, r.ProfitFactor, r.MaxDrawdown, r.NetProfit,
			r.Passed, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Backtests() ([]Backtest, error) {
	rows, err := s.db.Query(`
		SELECT symbol, strategy, trades, win_rate, profit_factor, max_drawdown, net_profit, passed, updated_at
		FROM backtest_results ORDER BY profit_factor DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Backtest
	for rows.Next() {
		var b Backtest
		var ts string
		if err := rows.Scan(&b.Symbol, &b.Strategy, &b.Trades, &b.WinRate, &b.ProfitFactor,
			&b.MaxDrawdown, &b.NetProfit, &b.Passed, &ts); err != nil {
			return nil, err
		}
		b.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, b)
	}
	return out, rows.Err()
}

// SaveSignal reports whether the action differs from what was stored, so the
// caller can notify on change instead of on every poll.
func (s *Store) SaveSignal(sig Signal) (changed bool, err error) {
	var prev string
	err = s.db.QueryRow(`SELECT action FROM signals WHERE symbol = ? AND strategy = ?`,
		sig.Symbol, sig.Strategy).Scan(&prev)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		changed = true
	case err != nil:
		return false, err
	default:
		changed = prev != sig.Action
	}

	_, err = s.db.Exec(`
		INSERT INTO signals (symbol, strategy, action, reason, price, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, strategy) DO UPDATE SET
		  action=excluded.action, reason=excluded.reason,
		  price=excluded.price, updated_at=excluded.updated_at`,
		sig.Symbol, sig.Strategy, sig.Action, sig.Reason, sig.Price,
		time.Now().UTC().Format(time.RFC3339))
	return changed, err
}

// Signals is keyed symbol then strategy; two symbols can run strategies that
// share a name, so a flat strategy key would silently drop one of them.
func (s *Store) Signals() (map[string]map[string]Signal, error) {
	rows, err := s.db.Query(`SELECT symbol, strategy, action, reason, price, updated_at FROM signals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]map[string]Signal{}
	for rows.Next() {
		var sig Signal
		var ts string
		if err := rows.Scan(&sig.Symbol, &sig.Strategy, &sig.Action, &sig.Reason, &sig.Price, &ts); err != nil {
			return nil, err
		}
		sig.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
		if out[sig.Symbol] == nil {
			out[sig.Symbol] = map[string]Signal{}
		}
		out[sig.Symbol][sig.Strategy] = sig
	}
	return out, rows.Err()
}

func (s *Store) OpenPosition(symbol, strategy, side string, entry, leverage float64) error {
	_, err := s.db.Exec(`
		INSERT INTO positions (symbol, strategy, side, entry_price, leverage, opened_at) VALUES (?, ?, ?, ?, ?, ?)`,
		symbol, strategy, side, entry, leverage, time.Now().UTC().Format(time.RFC3339))
	return err
}

// SetPositionAlertStep records the furthest 2%-of-leveraged-PnL milestone
// notified so far, ratcheted separately for the profit and loss directions so
// a retrace doesn't re-fire an already-seen level.
func (s *Store) SetPositionAlertStep(id int64, profitStep, lossStep int) error {
	_, err := s.db.Exec(`
		UPDATE positions SET profit_alert_step = ?, loss_alert_step = ? WHERE id = ?`,
		profitStep, lossStep, id)
	return err
}

// ClosePosition and CurrentPosition are scoped to one symbol: the "only one
// position open" rule exists to stop a symbol's four strategies contradicting
// each other, and says nothing about two independent markets.
func (s *Store) ClosePosition(symbol string) error {
	_, err := s.db.Exec(`
		UPDATE positions SET closed_at = ? WHERE closed_at IS NULL AND symbol = ?`,
		time.Now().UTC().Format(time.RFC3339), symbol)
	return err
}

func (s *Store) CurrentPosition(symbol string) (*Position, error) {
	var p Position
	var ts string
	err := s.db.QueryRow(`
		SELECT id, symbol, strategy, side, entry_price, leverage, profit_alert_step, loss_alert_step, opened_at
		FROM positions WHERE closed_at IS NULL AND symbol = ? ORDER BY id DESC LIMIT 1`, symbol).
		Scan(&p.ID, &p.Symbol, &p.Strategy, &p.Side, &p.EntryPrice, &p.Leverage, &p.ProfitAlertStep, &p.LossAlertStep, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.OpenedAt, _ = time.Parse(time.RFC3339, ts)
	return &p, nil
}

func (s *Store) Equity() (float64, error) {
	var v float64
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'equity'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

func (s *Store) SetEquity(v float64) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('equity', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, v)
	return err
}

// LastPrice is the reference price recorded at that symbol's last price alert.
// Zero means "no reference yet" (first run), not a real price. Scoped per symbol
// so each market's move watcher throttles on its own moves.
func (s *Store) LastPrice(symbol string) (float64, error) {
	var v float64
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, lastPriceKey(symbol)).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

func (s *Store) SetLastPrice(symbol string, v float64) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, lastPriceKey(symbol), v)
	return err
}

func lastPriceKey(symbol string) string { return "last_price:" + symbol }
