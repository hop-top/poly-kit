package qmochi

import (
	"errors"
	"fmt"
)

var (
	ErrEmptySeriesName     = errors.New("series name cannot be empty")
	ErrDuplicatePointLabel = errors.New("duplicate point label in series")
)

// Validate checks the chart configuration for errors.
func (c Chart) Validate() error {
	for i, s := range c.Series {
		if s.Name == "" {
			return fmt.Errorf("series %d: %w", i, ErrEmptySeriesName)
		}

		// Scatter uses X for positioning; labels are optional.
		if c.Type == ScatterChart {
			continue
		}

		labels := make(map[string]struct{})
		for _, p := range s.Points {
			if _, ok := labels[p.Label]; ok {
				return fmt.Errorf("series %q: %w: %q", s.Name, ErrDuplicatePointLabel, p.Label)
			}
			labels[p.Label] = struct{}{}
		}
	}
	return nil
}

// Normalize transforms the chart data into a consistent Dataset shape.
// It preserves series order and first-seen label order.
// Missing values for labels are filled with 0.
func Normalize(c Chart) (Dataset, error) {
	if err := c.Validate(); err != nil {
		return Dataset{}, err
	}

	// 1. Identify all unique labels in their first-seen order.
	var labels []string
	labelIdx := make(map[string]int)

	for _, s := range c.Series {
		for _, p := range s.Points {
			if _, ok := labelIdx[p.Label]; !ok {
				labelIdx[p.Label] = len(labels)
				labels = append(labels, p.Label)
			}
		}
	}

	// 2. Build normalized series.
	normalizedSeries := make([]Series, len(c.Series))
	for i, s := range c.Series {
		// Map existing points by label.
		pointMap := make(map[string]Point)
		for _, p := range s.Points {
			pointMap[p.Label] = p
		}

		// Create points for ALL labels in the canonical order.
		points := make([]Point, len(labels))
		for j, label := range labels {
			if p, ok := pointMap[label]; ok {
				points[j] = p
			} else {
				points[j] = Point{Label: label}
			}
		}

		normalizedSeries[i] = Series{
			Name:   s.Name,
			Points: points,
			Color:  s.Color,
			Style:  s.Style,
			Effect: s.Effect,
		}
	}

	return Dataset{
		Labels: labels,
		Series: normalizedSeries,
	}, nil
}
