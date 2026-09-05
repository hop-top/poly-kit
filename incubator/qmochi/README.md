# qmochi 🍡

> **Incubating.** Developed in
> [hop-top/poly-kit](https://github.com/hop-top/poly-kit/tree/main/incubator/qmochi);
> submit issues, PRs and discussions there.

Terminal charting for Go, built for
[Bubble Tea](https://github.com/charmbracelet/bubbletea). High-density
Unicode rendering with fractional blocks and Braille characters.
Value-oriented API: define, normalize, render. Optional SVG output.

```sh
go get hop.top/qmochi
```

## Chart types

| Type | Constant | Terminal | SVG | Description |
|------|----------|----------|-----|-------------|
| [Bar](../../docs/adopters/reference/qmochi-charts.md#bar-chart) | `BarChart` | `RenderBar` | `RenderSVG` | Horizontal bars, per-series styles |
| [Column](../../docs/adopters/reference/qmochi-charts.md#column-chart) | `ColumnChart` | `RenderColumn` | `RenderSVG` | Vertical columns, per-series styles |
| [Line](../../docs/adopters/reference/qmochi-charts.md#line-chart) | `LineChart` | `RenderLine` | `RenderSVG` | Points connected by line segments |
| [Sparkline](../../docs/adopters/reference/qmochi-charts.md#sparkline) | `SparklineChart` | `RenderSparkline` | `RenderSVG` | Ultra-compact single-row trend |
| [Heatmap](../../docs/adopters/reference/qmochi-charts.md#heatmap) | `HeatmapChart` | `RenderHeatmap` | `RenderSVG` | 2D grid with color/shade intensity |
| [Braille](../../docs/adopters/reference/qmochi-charts.md#braille-chart) | `BrailleChart` | `RenderLineBraille` | — | High-res 2D plotting (2x4 pixels) |
| [Scatter](../../docs/adopters/reference/qmochi-charts.md#scatter-chart) | `ScatterChart` | `RenderScatter` | `RenderSVG` | X/Y scatter with distinct markers |
| [Pie](../../docs/adopters/reference/qmochi-charts.md#pie-chart) | `PieChart` | `RenderPie` | `RenderSVG` | Circular proportions |

## Styles and effects

Per-chart or per-series; `Series.Style` overrides `Chart.Style`.
| Constant | Glyphs / type | Used in |
|----------|---------------|---------|
| `SolidBlock` | `▏▎▍▌▋▊▉█` | Bar, Column |
| `DottedBlock` | `· · · ·` | Bar (patterned) |
| `DashedBlock` | `╶─╼╸━` | Bar, Column |
| `ShadedBlock` | `░▒▓` | Bar (patterned) |
| `RoundedBlock` | `▂▃▄▅▆▇█` | Column |
| `BlinkEffect` | `Effect`, terminal blink | Heatmap |
| `CellGlyph` | `string`, override cell character | Heatmap |
| `Compact` | `bool`, half-block vertical packing | Heatmap |
| `DomainMin` | `*float64`, override Y-axis minimum | Bar, Column |

## Pipeline

```
data    → Auto(data) | AutoWithLLM(ctx, data, llm) → Chart
Chart   → Normalize(chart)                         → Dataset
Dataset → Render*(chart, ds, ly) | RenderSVG(chart, ds)
```

Raw series and points in; pick a chart type (or set one); normalize to
align series, fill gaps and validate; render to the terminal or to SVG.

## Bubble Tea integration

Stateful `Model` handles `tea.WindowSizeMsg` automatically. Dynamic
updates arrive as `qmochi.SetChartMsg` (replace chart config and data)
and `qmochi.SetSizeMsg` (override dimensions).

```go
m := qmochi.NewModel(chart)
m, cmd = m.Update(msg)   // in Update
m.View().Content         // in View
```

## Normalization rules

Preserves series order, style, color and effect; uses first-seen label
order across all series; zero-fills missing values; rejects empty series
names and duplicate labels. Scatter charts skip label validation, using
the X field instead.

## See also

- [qmochi chart gallery](../../docs/adopters/reference/qmochi-charts.md):
  every chart type with its options and worked example, SVG output,
  automatic and LLM-assisted chart selection

MIT licensed.
