# zec-signal

Signal bot for ZEC/USDT and BTC/USDT perpetual futures on Binance. Evaluates a set of tuned strategies on every 15-minute candle close, backtests them against 3 years of history on startup, serves a small web dashboard, and pushes Telegram alerts when a signal changes.

**This is an alert/dashboard tool, not an auto-trader.** It never places real orders — position tracking is just bookkeeping you drive from the dashboard so sizing and P&L context stay accurate.

## Features

- Independent strategy evaluation for ZEC and BTC (separate signals, positions, and price baselines per symbol)
- Seven strategies (trend, mean-reversion, breakout, momentum, regime-trend, VWAP-deviation, squeeze), each backtested with slippage/fee modeling and a walk-forward pass/fail gate (profit factor, expectancy, max drawdown)
- Telegram alerts (in Thai) on signal changes and on ±0.5% price moves, plus an hourly health check
- Local dashboard for equity, position sizing, and per-strategy backtest stats — all backed by a local SQLite file

## Requirements

- Go 1.26+
- A Telegram bot token + chat ID (optional — the bot runs fine without notifications configured)

## Setup

```bash
cp .env.example .env
# fill in TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID (optional)
```

## Running

```bash
go run ./cmd/zec-signal
```

On startup it:
1. Opens/creates `zec-signal.db` (SQLite)
2. Fetches 3 years of history per symbol and backtests all strategies (takes a bit — Binance history is paged in)
3. Starts the dashboard at `http://127.0.0.1:8722`
4. Runs the first strategy evaluation immediately, then on every 15m candle close

Flags:

```bash
go run ./cmd/zec-signal -addr 127.0.0.1:8722 -db zec-signal.db
```

## Dashboard

Open `http://127.0.0.1:8722`. For each symbol you can see current signals per strategy, backtest stats (trades, win rate, profit factor, expectancy, max drawdown, pass/fail), and position sizing based on your configured equity. Use the panel to set equity and open/close positions manually as you act on signals.

## Testing

```bash
go test ./...                  # fast, offline
ZEC_SWEEP=1 go test ./internal/backtest -run TestSweep -v   # re-run the parameter sweep (slow, hits Binance)
```

## Disclaimer

This is a personal signal tool, not financial advice. Backtest results (slippage/fee-adjusted, walk-forward gated) are shown for context, but past performance on 15m candles is not a guarantee of future results. Trade at your own risk.
