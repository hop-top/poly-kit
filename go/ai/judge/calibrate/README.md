# calibrate

## What it answers

How a tool corrects a judge's over- or under-confident probabilities after
the fact, using labelled samples, without changing the backend. Wrong
package when the backend's answers are already calibrated or carry no
probabilities.

## Use it when

- you fit parameters from labelled samples → `calibrate.Fit(samples, calibrate.FitOptions{Method: ...})`
- you scale a multi-label answer → `calibrate.Temperature{T}`
- you rescale a binary answer → `calibrate.Platt{A, B, Positive}`
- you store and reload parameters → `Params.Marshal`, `calibrate.Parse`
- you wrap a judge so every answer is calibrated → `calibrate.Calibrated(j, params)`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`calibrate.go`](calibrate.go) | `Temperature`, `Platt`, `Params`, `Apply` | you add a calibration method |
| [`fit.go`](fit.go) | `Fit`, `FitTemperature`, `FitPlatt` | you change how parameters are fitted |
| [`wrap.go`](wrap.go) | `Calibrated` judge wrapper | you change how answers are rewritten |
