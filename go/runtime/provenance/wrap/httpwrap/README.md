# httpwrap

## What it answers

A `*http.Client` wrapper that records URL and fetch time against the output field a response fills. Two constructors decide the source label: `New` for a live upstream, `NewCacheClient` when the inner client is itself a cache. Wrong package for values you derive rather than fetch (use `Tracker.Synthesize` in `runtime/provenance`).

## Use it when

- fetching a value you will emit as authoritative or `Cached[T]` → `c.Get(ctx, "/cohort", url)` or `c.ReadAll(ctx, "/cohort", url)`
- you already built the request → `c.Do(ctx, path, req)`
- the inner client serves from disk cache → `httpwrap.NewCacheClient(inner)`

## Quick start

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, "beta")
}))
defer srv.Close()

ctx := provenance.WithMode(context.Background(), provenance.ModeWarn) // ModeOff records nothing
c := httpwrap.New(http.DefaultClient)
body, prov, err := c.ReadAll(ctx, "/cohort", srv.URL+"/cohort")
if err != nil {
	panic(err)
}
out := struct {
	Cohort provenance.Cached[string] `json:"cohort"`
}{Cohort: provenance.NewCached(string(body), prov)}
fmt.Println(out.Cohort.Value(), prov.Source, prov.URL == srv.URL+"/cohort")
// beta authoritative true
```

Verified by `example_test.go` in this directory.

## Contract

- `path` is an RFC 6901 JSON pointer into the rendered `data` object.
- The recorded URL is normalised (lowercase scheme and host, default ports stripped, sorted query, no fragment).
- `Get` and `Do` return a body the caller must close; `ReadAll` closes it.
- The wrapper never owns the inner client's lifecycle.

## Neighbours

- `hop.top/kit/go/runtime/provenance`: wrappers, `Tracker`, `Render`, strict mode.
- `hop.top/kit/go/runtime/provenance/wrap/sqlwrap`, `.../execwrap`: the same stamp for SQL and subprocesses.
