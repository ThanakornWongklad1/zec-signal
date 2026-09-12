package indicators

import (
	"math"
	"testing"
)

func closeTo(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func isNaN(t *testing.T, name string, got float64) {
	t.Helper()
	if !math.IsNaN(got) {
		t.Errorf("%s = %v, want NaN", name, got)
	}
}

func TestSMA(t *testing.T) {
	got := SMA([]float64{1, 2, 3, 4, 5}, 3)
	isNaN(t, "sma[0]", got[0])
	isNaN(t, "sma[1]", got[1])
	closeTo(t, "sma[2]", got[2], 2)
	closeTo(t, "sma[3]", got[3], 3)
	closeTo(t, "sma[4]", got[4], 4)
}

func TestEMA(t *testing.T) {
	// period 3 -> k=0.5, seed = SMA(1,2,3) = 2 at index 2.
	got := EMA([]float64{1, 2, 3, 4, 5}, 3)
	isNaN(t, "ema[1]", got[1])
	closeTo(t, "ema[2]", got[2], 2)
	closeTo(t, "ema[3]", got[3], 3)
	closeTo(t, "ema[4]", got[4], 4)
}

func TestEMASkipsNaNPrefix(t *testing.T) {
	nan := math.NaN()
	got := EMA([]float64{nan, nan, 1, 2, 3, 4, 5}, 3)
	closeTo(t, "ema[4]", got[4], 2)
	closeTo(t, "ema[6]", got[6], 4)
}

func TestRSIWilder(t *testing.T) {
	// period 3 on 10,11,12,11,12,13 — hand-computed Wilder smoothing.
	got := RSI([]float64{10, 11, 12, 11, 12, 13}, 3)
	isNaN(t, "rsi[2]", got[2])
	closeTo(t, "rsi[3]", got[3], 100-100.0/3.0)  // rs = 2
	closeTo(t, "rsi[4]", got[4], 100-100.0/4.5)  // rs = 3.5
	closeTo(t, "rsi[5]", got[5], 100-100.0/6.75) // rs = 5.75
}

func TestRSIAllGains(t *testing.T) {
	got := RSI([]float64{1, 2, 3, 4, 5}, 3)
	closeTo(t, "rsi[4]", got[4], 100)
}

func TestBollinger(t *testing.T) {
	// mean 5, population sd 2 over the 8 samples.
	v := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	up, mid, low := Bollinger(v, 8, 2)
	closeTo(t, "mid", mid[7], 5)
	closeTo(t, "upper", up[7], 9)
	closeTo(t, "lower", low[7], 1)
	isNaN(t, "upper[6]", up[6])
}

func TestDonchian(t *testing.T) {
	highs := []float64{1, 3, 2, 5, 4}
	lows := []float64{1, 2, 1, 3, 2}
	up, low := Donchian(highs, lows, 3)
	isNaN(t, "up[1]", up[1])
	closeTo(t, "up[2]", up[2], 3)
	closeTo(t, "up[3]", up[3], 5)
	closeTo(t, "up[4]", up[4], 5)
	closeTo(t, "low[2]", low[2], 1)
	closeTo(t, "low[4]", low[4], 1)
}

func TestBBWidth(t *testing.T) {
	// Same fixture as TestBollinger: mid 5, upper 9, lower 1 -> (9-1)/5.
	got := BBWidth([]float64{2, 4, 4, 4, 5, 5, 7, 9}, 8, 2)
	closeTo(t, "width[7]", got[7], 1.6)
	isNaN(t, "width[6]", got[6])
}

func TestVWAP(t *testing.T) {
	// typical prices 1, 3, 5 with volumes 1, 1, 2; period 2.
	highs := []float64{2, 4, 6}
	lows := []float64{0, 2, 4}
	closes := []float64{1, 3, 5}
	vols := []float64{1, 1, 2}
	got := VWAP(highs, lows, closes, vols, 2)
	isNaN(t, "vwap[0]", got[0])
	closeTo(t, "vwap[1]", got[1], 2)        // (1*1 + 3*1) / 2
	closeTo(t, "vwap[2]", got[2], 13.0/3.0) // (3*1 + 5*2) / 3
}

func TestADXWilder(t *testing.T) {
	// period 2, hand-computed. Bar-by-bar (+DM, -DM, TR):
	//   i=1: 2, 0, 3    i=2: 0, 2, 4    i=3: 2, 0, 5    i=4: 0, 1, 3
	// seed sums tr=7 plus=2 minus=2 -> DX[2]=0; smoothing gives DX[3]=50,
	// DX[4]=0, so ADX[3]=mean(0,50)=25 and ADX[4]=(25*1+0)/2=12.5.
	highs := []float64{10, 12, 11, 13, 12}
	lows := []float64{8, 9, 7, 10, 9}
	closes := []float64{9, 11, 8, 12, 10}
	got := ADX(highs, lows, closes, 2)
	isNaN(t, "adx[2]", got[2])
	closeTo(t, "adx[3]", got[3], 25)
	closeTo(t, "adx[4]", got[4], 12.5)
}

func TestADXOneSidedTrendIsMax(t *testing.T) {
	// Every bar makes a higher high and higher low: -DM stays 0, so -DI is 0
	// and DX pins at 100 on every bar.
	var highs, lows, closes []float64
	for i := 0; i < 10; i++ {
		highs = append(highs, 10+float64(i))
		lows = append(lows, 8+float64(i))
		closes = append(closes, 9+float64(i))
	}
	got := ADX(highs, lows, closes, 3)
	closeTo(t, "adx[5]", got[5], 100)
	closeTo(t, "adx[9]", got[9], 100)
}

func TestADXFlatIsZero(t *testing.T) {
	v := []float64{5, 5, 5, 5, 5, 5, 5, 5}
	got := ADX(v, v, v, 3)
	closeTo(t, "adx[5]", got[5], 0)
	closeTo(t, "adx[7]", got[7], 0)
}

func TestMACDAlignment(t *testing.T) {
	v := make([]float64, 60)
	for i := range v {
		v[i] = 100 + float64(i) + math.Sin(float64(i))*3
	}
	macd, sig, hist := MACD(v, 3, 5, 3)
	ef, es := EMA(v, 3), EMA(v, 5)
	for i := range v {
		if math.IsNaN(ef[i]) || math.IsNaN(es[i]) {
			isNaN(t, "macd", macd[i])
			continue
		}
		closeTo(t, "macd", macd[i], ef[i]-es[i])
	}
	// signal only starts once 3 macd values exist: slow warm-up ends at index 4.
	isNaN(t, "sig[5]", sig[5])
	closeTo(t, "hist[10]", hist[10], macd[10]-sig[10])
}
