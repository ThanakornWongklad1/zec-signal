package market

import "time"

type Candle struct {
	OpenTime time.Time
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

func Closes(cs []Candle) []float64 { return field(cs, func(c Candle) float64 { return c.Close }) }
func Highs(cs []Candle) []float64  { return field(cs, func(c Candle) float64 { return c.High }) }
func Lows(cs []Candle) []float64   { return field(cs, func(c Candle) float64 { return c.Low }) }
func Volumes(cs []Candle) []float64 {
	return field(cs, func(c Candle) float64 { return c.Volume })
}

func field(cs []Candle, f func(Candle) float64) []float64 {
	out := make([]float64, len(cs))
	for i, c := range cs {
		out[i] = f(c)
	}
	return out
}
