package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"zec-signal/internal/backtest"
	"zec-signal/internal/binance"
	"zec-signal/internal/notify"
	"zec-signal/internal/store"
	"zec-signal/internal/strategy"
	"zec-signal/internal/web"
)

// symbols are watched independently: separate evaluations, separate positions,
// separate price-move references. Equity and the health check stay global —
// one account, one server process.
var symbols = []string{"ZECUSDT", "BTCUSDT"}

// base strips the quote currency for display: "BTCUSDT" -> "BTC".
func base(symbol string) string { return strings.TrimSuffix(symbol, "USDT") }

const (
	interval      = "15m"
	historyYears  = 3
	candlePeriod  = 15 * time.Minute
	publishBuffer = 20 * time.Second

	priceMovePct    = 0.005 // notify on every +/- move of this fraction from the last alert
	priceCheckEvery = 10 * time.Second

	pnlAlertPct = 0.02 // notify every 2% of leveraged PnL crossed, from the position's entry

	healthCheckEvery = time.Hour
)

func main() {
	if err := notify.LoadEnvFile(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}

	addr := flag.String("addr", "127.0.0.1:8722", "dashboard listen address")
	dbPath := flag.String("db", "zec-signal.db", "sqlite database path")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := binance.New()
	srv := &http.Server{Addr: *addr, Handler: web.New(st, symbols).Routes()}

	go func() {
		log.Printf("dashboard on http://%s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	for _, sym := range symbols {
		go runBacktest(client, st, sym)
		go watchPrice(ctx, client, st, sym)
	}
	go healthCheck(ctx, time.Now())

	evaluateAll(client, st)
	scheduler(ctx, client, st)

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

func scheduler(ctx context.Context, client *binance.Client, st *store.Store) {
	for {
		next := nextTick(time.Now())
		log.Printf("next evaluation at %s UTC", next.UTC().Format("15:04:05"))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			log.Print("shutting down")
			return
		case <-timer.C:
			evaluateAll(client, st)
		}
	}
}

// nextTick lands just after a 15m candle close so the exchange has published it.
func nextTick(now time.Time) time.Time {
	t := now.UTC().Truncate(candlePeriod).Add(candlePeriod + publishBuffer)
	for !t.After(now) {
		t = t.Add(candlePeriod)
	}
	return t
}

// evaluateAll runs every symbol; one symbol's failure (a bad response, a
// network blip) must not skip the others.
func evaluateAll(client *binance.Client, st *store.Store) {
	for _, sym := range symbols {
		if err := evaluate(client, st, sym); err != nil {
			log.Printf("%s evaluation failed: %v", sym, err)
		}
	}
}

func evaluate(client *binance.Client, st *store.Store, symbol string) error {
	cs, err := client.FuturesKlines(symbol, interval, strategy.Window+1)
	if err != nil {
		return err
	}
	if len(cs) < 2 {
		return fmt.Errorf("got %d candles, need history", len(cs))
	}
	cs = cs[:len(cs)-1] // the newest candle is still forming

	pos, err := st.CurrentPosition(symbol)
	if err != nil {
		return err
	}
	last := cs[len(cs)-1]

	bts, err := st.Backtests()
	if err != nil {
		return err
	}
	netProfit, passed := map[string]float64{}, map[string]bool{}
	for _, b := range bts {
		if b.Symbol != symbol {
			continue
		}
		netProfit[b.Strategy], passed[b.Strategy] = b.NetProfit, b.Passed
	}

	for _, s := range strategy.For(symbol) {
		var open *strategy.Open
		if pos != nil && pos.Strategy == s.Name() {
			open = &strategy.Open{
				Short:     pos.Side == "SHORT",
				Entry:     pos.EntryPrice,
				EntryTime: pos.OpenedAt,
			}
		}
		sig := s.Evaluate(cs, open)
		changed, err := st.SaveSignal(store.Signal{
			Symbol:   symbol,
			Strategy: s.Name(),
			Action:   string(sig.Action),
			Reason:   sig.Reason,
			Price:    last.Close,
		})
		if err != nil {
			return err
		}
		if changed && sig.Action != strategy.Hold {
			body := fmt.Sprintf("%s at %.2f — %s", sig.Action, last.Close, sig.Reason)
			log.Printf("%s %s: %s", symbol, s.Name(), body)

			// Only ping the phone for strategies whose backtest actually made
			// money — a net-losing strategy's live signal isn't worth a buzz.
			if net, ok := netProfit[s.Name()]; ok && net > 0 {
				title := fmt.Sprintf("%s — %s ที่ราคา %.2f", base(symbol), actionThai(sig.Action), last.Close)
				thaiBody := fmt.Sprintf("กลยุทธ์: %s\nคำแนะนำ: %s\nเหตุผล: %s",
					s.Name(), suggestionThai(passed[s.Name()], net), sig.Reason)
				if err := notify.Telegram(title, thaiBody); err != nil {
					log.Printf("notify: %v", err)
				}
			}
		}
	}
	return nil
}

// suggestionThai turns the backtest gate into plain advice, not a jargon
// PASS/FAIL label — the audience for this message doesn't know what a
// profit-factor or expectancy gate is, they need "should I act on this or not".
func suggestionThai(passed bool, net float64) string {
	if passed {
		return fmt.Sprintf("✅ แนะนำให้เทรดได้ (ผลทดสอบย้อนหลังกำไรสะสม +%.0f USDT)", net)
	}
	return fmt.Sprintf("⚠️ ยังไม่แนะนำ ควรระวังเป็นพิเศษ (ผลทดสอบย้อนหลังกำไรสะสม +%.0f USDT แต่ยังไม่ผ่านเกณฑ์ความน่าเชื่อถือ)", net)
}

// actionThai labels for Telegram messages; indicator jargon in sig.Reason
// stays in English (EMA/RSI/MACD read the same in Thai trading chat).
func actionThai(a strategy.Action) string {
	switch a {
	case strategy.Long:
		return "เปิด Long"
	case strategy.Short:
		return "เปิด Short"
	case strategy.Close:
		return "ปิดสถานะ"
	default:
		return string(a)
	}
}

// healthCheck pings once an hour so a silent server (crashed, network down,
// laptop asleep and just woken up) is distinguishable from one that's simply
// had nothing to signal — absence of alerts alone can't tell you that.
func healthCheck(ctx context.Context, startedAt time.Time) {
	ticker := time.NewTicker(healthCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uptime := time.Since(startedAt).Round(time.Minute)
			body := fmt.Sprintf("Signal server ทำงานปกติ (เปิดมาแล้ว %s)", uptime)
			if err := notify.Telegram("Health check", body); err != nil {
				log.Printf("notify: %v", err)
			}
			log.Printf("health check: uptime %s", uptime)
		}
	}
}

// watchPrice polls the live ticker independently of the 15m candle cycle, so
// price-level alerts fire within priceCheckEvery of a level being crossed
// instead of waiting for the next strategy evaluation.
func watchPrice(ctx context.Context, client *binance.Client, st *store.Store, symbol string) {
	ticker := time.NewTicker(priceCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			price, err := client.Price(symbol)
			if err != nil {
				log.Printf("%s price poll: %v", symbol, err)
				continue
			}
			if err := checkPriceMove(st, symbol, price); err != nil {
				log.Printf("%s price move check: %v", symbol, err)
			}
			if err := checkPositionPnL(st, symbol, price); err != nil {
				log.Printf("%s position pnl check: %v", symbol, err)
			}
		}
	}
}

// checkPriceMove notifies when price has moved +/-priceMovePct from the
// reference price recorded at the last alert (or the startup baseline).
// Resetting the reference on every trigger means the next alert always needs
// a fresh priceMovePct move from wherever price just was, so it self-throttles
// instead of flapping back and forth across a fixed boundary.
func checkPriceMove(st *store.Store, symbol string, price float64) error {
	ref, err := st.LastPrice(symbol)
	if err != nil {
		return err
	}
	if ref == 0 {
		return st.SetLastPrice(symbol, price)
	}
	change := (price - ref) / ref
	if math.Abs(change) < priceMovePct {
		return nil
	}
	dir, dirThai, emoji := "up", "ขึ้น", "🟢📈"
	if change < 0 {
		dir, dirThai, emoji = "down", "ลง", "🔴📉"
	}
	name := base(symbol)
	body := fmt.Sprintf("%s moved %.1f%% %s (now %.2f)", name, math.Abs(change)*100, dir, price)
	thaiBody := fmt.Sprintf("%s %s เปลี่ยนแปลง %.1f%% %s (ตอนนี้ %.2f)", emoji, name, math.Abs(change)*100, dirThai, price)
	if err := notify.Telegram(emoji+" แจ้งเตือนราคา "+name, thaiBody); err != nil {
		log.Printf("notify: %v", err)
	}
	log.Print(body)
	return st.SetLastPrice(symbol, price)
}

// checkPositionPnL notifies as an open position's leveraged PnL crosses each
// pnlAlertPct milestone from entry (2%, 4%, 6%, ...). Profit and loss each
// ratchet independently so a retrace doesn't re-fire an already-seen level,
// and a jump past several levels at once only notifies for the level reached.
func checkPositionPnL(st *store.Store, symbol string, price float64) error {
	pos, err := st.CurrentPosition(symbol)
	if err != nil || pos == nil {
		return err
	}
	change := (price - pos.EntryPrice) / pos.EntryPrice
	if pos.Side == "SHORT" {
		change = -change
	}
	pnlPct := change * pos.Leverage

	step := int(math.Abs(pnlPct) / pnlAlertPct)
	profitStep, lossStep := pos.ProfitAlertStep, pos.LossAlertStep
	if pnlPct >= 0 {
		if step <= profitStep {
			return nil
		}
		profitStep = step
	} else {
		if step <= lossStep {
			return nil
		}
		lossStep = step
	}

	name := base(symbol)
	dirThai, emoji := "กำไร", "🟢"
	if pnlPct < 0 {
		dirThai, emoji = "ขาดทุน", "🔴"
	}
	thaiBody := fmt.Sprintf("%s %s %s %s: %.1f%% (ราคาเข้า %.2f, ตอนนี้ %.2f, leverage %.0fx)",
		emoji, name, pos.Side, dirThai, math.Abs(pnlPct)*100, pos.EntryPrice, price, pos.Leverage)
	if err := notify.Telegram(emoji+" PnL "+name, thaiBody); err != nil {
		log.Printf("notify: %v", err)
	}
	log.Printf("%s position pnl %.1f%%", symbol, pnlPct*100)
	return st.SetPositionAlertStep(pos.ID, profitStep, lossStep)
}

func runBacktest(client *binance.Client, st *store.Store, symbol string) {
	log.Printf("%s: fetching %dy of spot history for backtest", symbol, historyYears)
	cs, err := client.SpotHistory(symbol, interval, time.Now().AddDate(-historyYears, 0, 0))
	if err != nil {
		log.Printf("%s backtest history: %v", symbol, err)
		return
	}
	log.Printf("%s: backtesting %d candles", symbol, len(cs))

	var rows []store.Backtest
	for _, r := range backtest.RunAll(symbol, cs) {
		log.Printf("%s %-24s trades=%-5d wr=%.1f%% pf=%.2f exp=%.3fR maxDD=%.1f%% net=%.0f pass=%v",
			symbol, r.Strategy, r.Trades, r.WinRate*100, r.ProfitFactor, r.Expectancy, r.MaxDrawdown*100, r.NetProfit, r.Passed)
		rows = append(rows, store.Backtest{
			Symbol: symbol, Strategy: r.Strategy, Trades: r.Trades, WinRate: r.WinRate,
			ProfitFactor: r.ProfitFactor, MaxDrawdown: r.MaxDrawdown,
			NetProfit: r.NetProfit, Passed: r.Passed,
		})
	}
	if err := st.SaveBacktests(rows); err != nil {
		log.Printf("%s save backtests: %v", symbol, err)
	}
}
