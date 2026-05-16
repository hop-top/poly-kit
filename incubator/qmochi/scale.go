package qmochi

import (
	"math"
	"strconv"
)

// Domain represents the extent of data values.
type Domain struct {
	Min float64
	Max float64
}

// DomainFor calculates the domain (min and max values) for a given dataset.
func DomainFor(ds Dataset) Domain {
	if len(ds.Series) == 0 {
		return Domain{0, 0}
	}

	min := math.MaxFloat64
	max := -math.MaxFloat64
	hasData := false

	for _, s := range ds.Series {
		for _, p := range s.Points {
			if p.Value < min {
				min = p.Value
			}
			if p.Value > max {
				max = p.Value
			}
			hasData = true
		}
	}

	if !hasData {
		return Domain{0, 0}
	}

	return Domain{Min: min, Max: max}
}

// Tick represents a mark on an axis.
type Tick struct {
	Value float64
	Label string
}

// NiceTicks generates a set of human-readable tick marks for a domain.
func NiceTicks(d Domain, maxTicks int) []Tick {
	if maxTicks <= 1 {
		return nil
	}

	span := d.Max - d.Min
	if span == 0 {
		if d.Min == 0 {
			return []Tick{{Value: 0, Label: "0"}}
		}
		return []Tick{{Value: d.Min, Label: strconv.FormatFloat(d.Min, 'g', -1, 64)}}
	}

	// Calculate nice step size
	rawStep := span / float64(maxTicks-1)
	mag := math.Pow(10, math.Floor(math.Log10(rawStep)))
	res := rawStep / mag

	var step float64
	switch {
	case res < 1.5:
		step = 1 * mag
	case res < 3:
		step = 2 * mag
	case res < 7:
		step = 5 * mag
	default:
		step = 10 * mag
	}

	start := math.Floor(d.Min/step) * step
	end := math.Ceil(d.Max/step) * step

	// Determine decimal precision from step size.
	prec := 0
	if step < 1 {
		prec = int(math.Ceil(-math.Log10(step)))
	}

	var ticks []Tick
	for v := start; v <= end+step/2; v += step {
		// Round to step precision to avoid FP noise (e.g. 0.6000000000000001).
		rounded := math.Round(v/step) * step
		label := strconv.FormatFloat(rounded, 'f', prec, 64)
		ticks = append(ticks, Tick{
			Value: rounded,
			Label: label,
		})
	}

	return ticks
}
