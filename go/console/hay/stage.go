package hay

type Stage[T any] struct {
	Name   string
	Lookup func(query string) []T
}

func ResolveStaged[T any](query string, stages []Stage[T], opts Options[T]) (Result[T], error) {
	for _, stage := range stages {
		items := stage.Lookup(query)
		if len(items) > 0 {
			return Resolve(query, items, opts)
		}
	}
	return Result[T]{}, &ErrNoMatch{Query: query}
}
