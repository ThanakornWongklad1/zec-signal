package indicators

import "math"

// All indicators return a slice the same length as the input, with NaN in the
// warm-up region, so callers can index by candle position without offset math.

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}

func SMA(v []float64, period int) []float64 {
	out := nanSlice(len(v))
	if period <= 0 || len(v) < period {
		return out
	}
	sum := 0.0
	for i, x := range v {
		sum += x
		if i >= period {
			sum -= v[i-period]
		}
		if i >= period-1 {
			out[i] = sum / float64(period)
		}
	}
	return out
}

func EMA(v []float64, period int) []float64 {
	out := nanSlice(len(v))
	if period <= 0 {
		return out
	}
	start := 0
	for start < len(v) && math.IsNaN(v[start]) {
		start++
	}
	if len(v)-start < period {
		return out
	}
	sum := 0.0
	for i := start; i < start+period; i++ {
		sum += v[i]
	}
	out[start+period-1] = sum / float64(period)
	k := 2.0 / float64(period+1)
	for i := start + period; i < len(v); i++ {
		out[i] = (v[i]-out[i-1])*k + out[i-1]
	}
	return out
}

// RSI uses Wilder's smoothing, matching what exchanges and TradingView show.
func RSI(v []float64, period int) []float64 {
	out := nanSlice(len(v))
	if period <= 0 || len(v) <= period {
		return out
	}
	var gain, loss float64
	for i := 1; i <= period; i++ {
		d := v[i] - v[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain /= float64(period)
	loss /= float64(period)
	out[period] = rsiValue(gain, loss)
	p := float64(period)
	for i := period + 1; i < len(v); i++ {
		d := v[i] - v[i-1]
		up, down := 0.0, 0.0
		if d > 0 {
			up = d
		} else {
			down = -d
		}
		gain = (gain*(p-1) + up) / p
		loss = (loss*(p-1) + down) / p
		out[i] = rsiValue(gain, loss)
	}
	return out
}

func rsiValue(gain, loss float64) float64 {
	if loss == 0 {
		if gain == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+gain/loss)
}

func Bollinger(v []float64, period int, k float64) (upper, mid, lower []float64) {
	mid = SMA(v, period)
	upper, lower = nanSlice(len(v)), nanSlice(len(v))
	for i := period - 1; i < len(v); i++ {
		if i < 0 || math.IsNaN(mid[i]) {
			continue
		}
		variance := 0.0
		for j := i - period + 1; j <= i; j++ {
			d := v[j] - mid[i]
			variance += d * d
		}
		sd := math.Sqrt(variance / float64(period))
		upper[i] = mid[i] + k*sd
		lower[i] = mid[i] - k*sd
	}
	return upper, mid, lower
}

func Donchian(highs, lows []float64, period int) (upper, lower []float64) {
	upper, lower = nanSlice(len(highs)), nanSlice(len(highs))
	if period <= 0 {
		return upper, lower
	}
	for i := period - 1; i < len(highs); i++ {
		hi, lo := highs[i], lows[i]
		for j := i - period + 1; j <= i; j++ {
			hi = math.Max(hi, highs[j])
			lo = math.Min(lo, lows[j])
		}
		upper[i], lower[i] = hi, lo
	}
	return upper, lower
}

// BBWidth is Bollinger band width normalised by the mid band — the standard
// squeeze measure, comparable across price scales and across symbols.
func BBWidth(v []float64, period int, k float64) []float64 {
	upper, mid, lower := Bollinger(v, period, k)
	out := nanSlice(len(v))
	for i := range v {
		if !math.IsNaN(upper[i]) && mid[i] != 0 {
			out[i] = (upper[i] - lower[i]) / mid[i]
		}
	}
	return out
}

// VWAP is the rolling volume-weighted average typical price over the last
// `period` candles — a mean-reversion reference that weights the levels where
// size actually traded, unlike an SMA.
func VWAP(highs, lows, closes, volumes []float64, period int) []float64 {
	out := nanSlice(len(closes))
	if period <= 0 || len(closes) < period {
		return out
	}
	var pv, vol float64
	for i := range closes {
		pv += typical(highs, lows, closes, i) * volumes[i]
		vol += volumes[i]
		if j := i - period; j >= 0 {
			pv -= typical(highs, lows, closes, j) * volumes[j]
			vol -= volumes[j]
		}
		if i >= period-1 && vol > 0 {
			out[i] = pv / vol
		}
	}
	return out
}

func typical(highs, lows, closes []float64, i int) float64 {
	return (highs[i] + lows[i] + closes[i]) / 3
}

// ADX is Wilder's Average Directional Index: trend *strength* regardless of
// direction, used to tell a trending regime from a chopping one. Wilder
// smooths twice, so the first value only lands at index 2*period-1.
func ADX(highs, lows, closes []float64, period int) []float64 {
	out := nanSlice(len(highs))
	if period <= 0 || len(highs) < 2*period {
		return out
	}
	var tr, plus, minus float64
	for i := 1; i <= period; i++ {
		t, p, m := directional(highs, lows, closes, i)
		tr, plus, minus = tr+t, plus+p, minus+m
	}
	p := float64(period)
	dx := nanSlice(len(highs))
	dx[period] = dxValue(tr, plus, minus)
	for i := period + 1; i < len(highs); i++ {
		t, pm, mm := directional(highs, lows, closes, i)
		tr = tr - tr/p + t
		plus = plus - plus/p + pm
		minus = minus - minus/p + mm
		dx[i] = dxValue(tr, plus, minus)
	}
	sum := 0.0
	for i := period; i < 2*period; i++ {
		sum += dx[i]
	}
	out[2*period-1] = sum / p
	for i := 2 * period; i < len(highs); i++ {
		out[i] = (out[i-1]*(p-1) + dx[i]) / p
	}
	return out
}

// directional returns bar i's true range and its directional movement, where
// only the larger of the two moves counts — an inside bar contributes neither.
func directional(highs, lows, closes []float64, i int) (tr, plus, minus float64) {
	up, down := highs[i]-highs[i-1], lows[i-1]-lows[i]
	if up > down && up > 0 {
		plus = up
	}
	if down > up && down > 0 {
		minus = down
	}
	tr = math.Max(highs[i]-lows[i], math.Max(math.Abs(highs[i]-closes[i-1]), math.Abs(lows[i]-closes[i-1])))
	return tr, plus, minus
}

func dxValue(tr, plus, minus float64) float64 {
	if tr == 0 {
		return 0
	}
	pdi, mdi := 100*plus/tr, 100*minus/tr
	if pdi+mdi == 0 {
		return 0
	}
	return 100 * math.Abs(pdi-mdi) / (pdi + mdi)
}

func MACD(v []float64, fast, slow, signal int) (macd, signalLine, hist []float64) {
	ef, es := EMA(v, fast), EMA(v, slow)
	macd = nanSlice(len(v))
	for i := range v {
		if !math.IsNaN(ef[i]) && !math.IsNaN(es[i]) {
			macd[i] = ef[i] - es[i]
		}
	}
	signalLine = EMA(macd, signal)
	hist = nanSlice(len(v))
	for i := range v {
		if !math.IsNaN(macd[i]) && !math.IsNaN(signalLine[i]) {
			hist[i] = macd[i] - signalLine[i]
		}
	}
	return macd, signalLine, hist
}
