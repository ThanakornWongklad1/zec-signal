package web

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"zec-signal/internal/backtest"
	"zec-signal/internal/risk"
	"zec-signal/internal/store"
	"zec-signal/internal/strategy"
)

//go:embed templates/*.html
var files embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"pct":  func(v float64) string { return strconv.FormatFloat(v*100, 'f', 1, 64) + "%" },
	"base": func(symbol string) string { return strings.TrimSuffix(symbol, "USDT") },
}).ParseFS(files, "templates/*.html"))

type Server struct {
	st      *store.Store
	symbols []string
}

func New(st *store.Store, symbols []string) *Server { return &Server{st: st, symbols: symbols} }

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.page)
	mux.HandleFunc("GET /panel", s.panel)
	mux.HandleFunc("POST /equity", s.setEquity)
	mux.HandleFunc("POST /position/open", s.openPosition)
	mux.HandleFunc("POST /position/close", s.closePosition)
	return mux
}

type row struct {
	Strategy     string
	Action       string
	Reason       string
	Trades       int
	WinRate      float64
	ProfitFactor float64
	MaxDrawdown  float64
	NetProfit    float64
	Passed       bool
	HasBacktest  bool
}

// section is one symbol's slice of the dashboard. Price, sizing and the open
// position are all per-symbol; equity and the gate are account-wide.
type section struct {
	Symbol   string
	Rows     []row
	Position *store.Position
	Price    float64
	Size     risk.Position
	StopPct  float64
}

type view struct {
	Sections []section
	Equity   float64
	Gate     gate
	Leverage float64
}

type gate struct {
	ProfitFactor float64
	Expectancy   float64
	MaxDrawdown  float64
}

func (s *Server) build() (*view, error) {
	bts, err := s.st.Backtests()
	if err != nil {
		return nil, err
	}
	sigs, err := s.st.Signals()
	if err != nil {
		return nil, err
	}
	equity, err := s.st.Equity()
	if err != nil {
		return nil, err
	}

	byKey := map[string]store.Backtest{}
	for _, b := range bts {
		byKey[b.Symbol+" "+b.Strategy] = b
	}

	v := &view{
		Equity:   equity,
		Gate:     gate{backtest.GateProfitFactor, backtest.GateExpectancy, backtest.GateMaxDrawdown},
		Leverage: risk.Leverage,
	}
	for _, symbol := range s.symbols {
		pos, err := s.st.CurrentPosition(symbol)
		if err != nil {
			return nil, err
		}
		sec := section{Symbol: symbol, Position: pos, StopPct: risk.StopLossPct}

		stops := map[string]float64{}
		for _, st := range strategy.For(symbol) {
			name := st.Name()
			stops[name] = st.StopPct()
			r := row{Strategy: name, Action: "—", Reason: "awaiting first evaluation"}
			if sig, okSig := sigs[symbol][name]; okSig {
				r.Action, r.Reason = sig.Action, sig.Reason
				if sig.Price > 0 {
					sec.Price = sig.Price
				}
			}
			if b, okBt := byKey[symbol+" "+name]; okBt {
				r.HasBacktest = true
				r.Trades, r.WinRate, r.ProfitFactor = b.Trades, b.WinRate, b.ProfitFactor
				r.MaxDrawdown, r.NetProfit, r.Passed = b.MaxDrawdown, b.NetProfit, b.Passed
			}
			sec.Rows = append(sec.Rows, r)
		}
		// Ranked by profit factor so the strategy currently earning the most sits on top.
		for i := 1; i < len(sec.Rows); i++ {
			for j := i; j > 0 && sec.Rows[j].ProfitFactor > sec.Rows[j-1].ProfitFactor; j-- {
				sec.Rows[j], sec.Rows[j-1] = sec.Rows[j-1], sec.Rows[j]
			}
		}

		// Stop distance is per-strategy, so the sizing panel follows the open
		// position and otherwise the top-ranked strategy.
		if len(sec.Rows) > 0 {
			sec.StopPct = stops[sec.Rows[0].Strategy]
		}
		if pos != nil {
			if p, okStop := stops[pos.Strategy]; okStop {
				sec.StopPct = p
			}
		}
		if equity > 0 && sec.Price > 0 {
			sec.Size = risk.Size(equity, sec.Price, sec.StopPct, pos != nil && pos.Side == "SHORT")
		}
		v.Sections = append(v.Sections, sec)
	}
	return v, nil
}

func (s *Server) render(w http.ResponseWriter, name string) {
	v, err := s.build()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, "page.html")
}

func (s *Server) panel(w http.ResponseWriter, r *http.Request) { s.render(w, "panel") }

func (s *Server) setEquity(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.ParseFloat(r.FormValue("equity"), 64)
	if err != nil || v < 0 {
		http.Error(w, "equity must be a non-negative number", http.StatusBadRequest)
		return
	}
	if err := s.st.SetEquity(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "panel")
}

func (s *Server) openPosition(w http.ResponseWriter, r *http.Request) {
	symbol, ok := s.symbol(r)
	if !ok {
		http.Error(w, "unknown symbol", http.StatusBadRequest)
		return
	}
	side := r.FormValue("side")
	if side != "LONG" && side != "SHORT" {
		http.Error(w, "side must be LONG or SHORT", http.StatusBadRequest)
		return
	}
	entry, err := strconv.ParseFloat(r.FormValue("entry_price"), 64)
	if err != nil || entry <= 0 {
		http.Error(w, "entry_price must be a positive number", http.StatusBadRequest)
		return
	}
	leverage, err := strconv.ParseFloat(r.FormValue("leverage"), 64)
	if err != nil || leverage <= 0 {
		http.Error(w, "leverage must be a positive number", http.StatusBadRequest)
		return
	}
	name := r.FormValue("strategy")
	valid := false
	for _, st := range strategy.For(symbol) {
		if st.Name() == name {
			valid = true
		}
	}
	if !valid {
		http.Error(w, "unknown strategy", http.StatusBadRequest)
		return
	}
	if err := s.st.OpenPosition(symbol, name, side, entry, leverage); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "panel")
}

func (s *Server) closePosition(w http.ResponseWriter, r *http.Request) {
	symbol, ok := s.symbol(r)
	if !ok {
		http.Error(w, "unknown symbol", http.StatusBadRequest)
		return
	}
	if err := s.st.ClosePosition(symbol); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "panel")
}

// symbol accepts only symbols this server actually watches, so a posted value
// can never reach the store as an unwatched symbol nobody would ever see again.
func (s *Server) symbol(r *http.Request) (string, bool) {
	v := r.FormValue("symbol")
	for _, sym := range s.symbols {
		if sym == v {
			return v, true
		}
	}
	return "", false
}
