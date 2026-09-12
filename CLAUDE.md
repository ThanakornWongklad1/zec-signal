# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single Go binary that watches ZEC/USDT and BTC/USDT perpetuals on Binance, evaluates a fixed set of tuned strategies every 15m candle close, persists signals/backtests/positions to SQLite, serves a small dashboard, and pushes Telegram alerts (in Thai) on signal changes and price moves.

## Commands

```bash
go build ./...                 # compile
go run ./cmd/zec-signal         # run (reads .env, opens zec-signal.db, serves on 127.0.0.1:8722)
go test ./...                   # unit tests (fast, offline)
go test ./internal/strategy -run TestName   # single test
ZEC_SWEEP=1 go test ./internal/backtest -run TestSweep -v   # parameter sweep (fetches 3y of history from Binance, slow, network-bound; results cached to $TMPDIR per symbol/interval)
```

No linter/formatter config beyond `gofmt`/`go vet` — nothing else is wired in.

## Architecture

Evaluation flow, one cycle:

1. `cmd/zec-signal/main.go` schedules `evaluateAll` just after each 15m candle closes (`nextTick`), plus an independent `watchPrice` poller (10s) per symbol for price-move alerts, and an hourly `healthCheck`.
2. `evaluate()` fetches the latest candles via `internal/binance`, drops the still-forming candle, loads the open `store.Position` (if any) and cached `store.Backtest` rows, then runs every `strategy.For(symbol)` strategy against the window.
3. Each `strategy.Strategy.Evaluate(candles, openPosition)` returns a `Signal{Action, Reason, StopHit}`. `store.SaveSignal` reports whether the action changed since last time — only changes trigger a Telegram push, and only for strategies whose cached backtest is net-profitable (`net > 0`).
4. `internal/risk.Size` derives position size from equity/stop distance for display; the bot does not place real orders anywhere — it's alert/dashboard only, position tracking (`store.OpenPosition`/`ClosePosition`) is driven by the human via the dashboard.

Strategies are two symbols run independently end-to-end: separate strategy sets, separate open positions, separate price-move baselines. Only equity and the health check are account-wide (see the `symbols` var comment in `main.go`).

### Strategy layer (`internal/strategy`)

- `Strategy` interface: `Name()`, `StopPct()`, `Evaluate(cs []market.Candle, pos *Open) Signal`.
- Concrete strategies (`trend.go`, `meanreversion.go`, `breakout.go`, `momentum.go`, `regimetrend.go`, `vwapdeviation.go`, `squeeze.go`) each embed an `Exit{Stop, Hold}` and call the shared `Exit.sharedExit` for stop-loss / time-stop / reversal-close so exit logic can't drift between them.
- `tuned` in `strategy.go` is the **only** place parameter sets are defined, per symbol, hand-picked from `sweep_test.go` runs against that symbol's own 3y history — parameters are not portable across symbols. The big comment above `tuned` records which entries actually pass on both the training window and the untouched holdout (walk-forward) vs which are memorized/losing but kept applied so the dashboard shows honest numbers. Read that comment before touching gate thresholds or adding a strategy — it explains why some strategies stay wired in despite failing.
- `Window = 150` trailing candles; any `Slow`/`Period` param in a tuned entry must stay under this or the strategy silently goes flat.

### Backtest gate (`internal/backtest`)

- `Run` replays one strategy bar-by-bar with slippage (`SlippagePct`) and taker fees (`TakerFeePct`) baked into fills.
- `RunAll` requires the walk-forward split (`Split`: first 2/3 train, last 1/3 holdout) to **both** pass the gate (`GateProfitFactor`, `GateExpectancy`, `GateMaxDrawdown`) — passing only on the full/training history is treated as in-sample noise, not a real edge. `Passed` on the whole-history `Result` gets overwritten by this walk-forward verdict.
- `main.go`'s `runBacktest` re-fetches 3y of spot history and reruns this at every process start (not just during sweeps) — it's how `store.Backtest` rows stay current, and it's what `evaluate()` checks before allowing a Telegram push.

### Storage (`internal/store`)

- Single SQLite file, tables: `backtest_results`, `signals`, `positions` (all keyed by `symbol` + `strategy` where relevant), `settings` (equity, per-symbol last-alert-price).
- `migrate()` handles the one-time upgrade from a pre-multi-symbol schema (tables without a `symbol` column, implicitly all ZEC) — safe no-op on current databases. If you add a new persisted field, follow the existing rename-recreate-copy pattern rather than a bare `ALTER TABLE` when a primary key needs to widen.

### Dashboard (`internal/web`)

- Plain `html/template` (`templates/*.html`, embedded via `go:embed`), no JS framework. `GET /panel` re-renders just the panel fragment for htmx-style polling; POST handlers (`/equity`, `/position/open`, `/position/close`) mutate the store then re-render the same fragment.
- `symbol()` validates posted symbol values against the server's own watched-symbol list before touching the store.

### Notifications (`internal/notify`)

- `LoadEnvFile` is a minimal `.env` parser (no external dep) — existing process env always wins over the file. `telegram.go` sends via the Bot API using `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID`; alert copy is Thai-language, aimed at a non-technical reader (see `suggestionThai`/`actionThai` in `main.go` — jargon like "profit factor" gets translated into plain "should you act on this" language).

## Conventions worth preserving

- Comments in this codebase explain *why*, often with specific numbers/history (e.g. slippage assumptions, gate threshold derivations, which strategies are memorized vs validated) — match that style rather than removing context when editing nearby code.
- New strategies must go through the sweep → walk-forward-gate pipeline before being wired into `tuned`; don't hand-pick parameters without a holdout check.
