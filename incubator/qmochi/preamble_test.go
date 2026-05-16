package qmochi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteHeader_TitleOnly(t *testing.T) {
	var b strings.Builder
	writeHeader(&b, Chart{Title: "Sales"})
	out := b.String()

	assert.Contains(t, out, "Sales")
	assert.Equal(t, 1, strings.Count(out, "\n"))
}

func TestWriteHeader_TitleAndSubtitle(t *testing.T) {
	var b strings.Builder
	writeHeader(&b, Chart{Title: "Sales", Subtitle: "Q1 2026"})
	out := b.String()

	assert.Contains(t, out, "Sales")
	assert.Contains(t, out, "Q1 2026")
	assert.Equal(t, 2, strings.Count(out, "\n"))
}

func TestWriteHeader_Empty(t *testing.T) {
	var b strings.Builder
	writeHeader(&b, Chart{})

	assert.Empty(t, b.String())
}

func TestChartDomain_ZeroBasedBar(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 10}, {Value: 20}},
	}}}

	d, span := chartDomain(Chart{Type: BarChart}, ds)

	assert.Equal(t, 0.0, d.Min)
	assert.Equal(t, 20.0, d.Max)
	assert.Equal(t, 20.0, span)
}

func TestChartDomain_ZeroBasedColumn(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 5}, {Value: 15}},
	}}}

	d, span := chartDomain(Chart{Type: ColumnChart}, ds)

	assert.Equal(t, 0.0, d.Min)
	assert.Equal(t, 15.0, d.Max)
	assert.Equal(t, 15.0, span)
}

func TestChartDomain_NonBarPreservesMin(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 10}, {Value: 20}},
	}}}

	d, span := chartDomain(Chart{Type: LineChart}, ds)

	assert.Equal(t, 10.0, d.Min)
	assert.Equal(t, 20.0, d.Max)
	assert.Equal(t, 10.0, span)
}

func TestChartDomain_DomainMinOverride(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 10}, {Value: 20}},
	}}}

	min := 5.0
	d, span := chartDomain(Chart{Type: BarChart, DomainMin: &min}, ds)

	assert.Equal(t, 5.0, d.Min)
	assert.Equal(t, 20.0, d.Max)
	assert.Equal(t, 15.0, span)
}

func TestChartDomain_DomainMinOverridesZeroBasing(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 10}, {Value: 20}},
	}}}

	// DomainMin=10 should override the default zero-basing for bar
	min := 10.0
	d, _ := chartDomain(Chart{Type: BarChart, DomainMin: &min}, ds)

	assert.Equal(t, 10.0, d.Min)
}

func TestChartDomain_NegativeMinUntouched(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: -5}, {Value: 10}},
	}}}

	d, span := chartDomain(Chart{Type: BarChart}, ds)

	assert.Equal(t, -5.0, d.Min)
	assert.Equal(t, 10.0, d.Max)
	assert.Equal(t, 15.0, span)
}

func TestChartDomain_ZeroSpanClampedToOne(t *testing.T) {
	ds := Dataset{Series: []Series{{
		Name:   "S",
		Points: []Point{{Value: 5}, {Value: 5}},
	}}}

	_, span := chartDomain(Chart{Type: LineChart}, ds)

	assert.Equal(t, 1.0, span)
}
