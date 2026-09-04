package serve

import "fmt"

// StartOrder returns selected in topological order over the optional
// [Dependent] declarations, ties broken by the order in selected
// (which [Resolve] already returns in registration order) — contract
// §"Ordering".
//
// A dependency naming a service outside selected is ignored rather
// than an error: under the selector form exactly one service runs, and
// its dependencies are the operator's business, not a reason to refuse
// a deliberate single-service start.
//
// A dependency cycle panics, in the same class as a name collision: it
// is a wiring bug in main that can only be fixed by editing the
// registrations, and there is no order the supervisor could pick that
// would be correct.
func StartOrder(reg *Registry, selected []string) []string {
	inSet := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		inSet[name] = struct{}{}
	}

	deps := make(map[string][]string, len(selected))
	for _, name := range selected {
		svc, ok := reg.Lookup(name)
		if !ok {
			continue
		}
		d, ok := svc.(Dependent)
		if !ok {
			continue
		}
		for _, want := range d.DependsOn() {
			if _, member := inSet[want]; member && want != name {
				deps[name] = append(deps[name], want)
			}
		}
	}

	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // emitted
	)
	mark := make(map[string]int, len(selected))
	out := make([]string, 0, len(selected))

	var visit func(string, []string)
	visit = func(name string, path []string) {
		switch mark[name] {
		case black:
			return
		case grey:
			panic(fmt.Sprintf("serve: dependency cycle: %v -> %s", path, name))
		}
		mark[name] = grey
		for _, want := range deps[name] {
			visit(want, append(path, name))
		}
		mark[name] = black
		out = append(out, name)
	}

	for _, name := range selected {
		visit(name, nil)
	}
	return out
}
