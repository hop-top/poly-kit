package cli

import (
	"context"
	"sort"
	"sync"
)

// Service status providers.
//
// `service status` reports kit's fixed rows — each service the
// running instance started, its readiness, address and pid — and,
// below them, sections the tool contributes about the same instance:
// queue depths, the profile it serves, per-channel health. They are
// [StatusProvider]s, the type the root `status` runs, registered here
// and run by the instance itself, in the `service start` process, when
// a client asks: what they read is the running instance's state, not
// the client's. The registry stays in cli so a tool can register its
// sections whether or not it links the serve plane.

// RegisterServiceStatusProvider records fn under name as a section of
// `service status`. Last write wins. Register before Execute.
//
// The running instance calls fn on each `service status` (and once
// when `service restart` reports the new run), with a deadline of a
// few seconds; a provider that misses it is reported unavailable, one
// that fails or panics as an error section. Sections marked Sensitive
// are redacted with the root status's patterns (the kit defaults plus
// StatusConfig.RedactPatterns); `service status` has no flag that
// reveals them. These providers feed `service status` only; the root
// `status` keeps its own, registered with [Root.RegisterStatusProvider].
func (r *Root) RegisterServiceStatusProvider(name string, fn StatusProvider) {
	if r == nil || name == "" || fn == nil {
		return
	}
	r.statusProvidersMu.Lock()
	defer r.statusProvidersMu.Unlock()
	if r.serviceStatusProviders == nil {
		r.serviceStatusProviders = make(map[string]StatusProvider)
	}
	r.serviceStatusProviders[name] = fn
}

// ServiceStatusSections runs every provider registered with
// [Root.RegisterServiceStatusProvider] at once, each under the status
// pipeline's per-provider deadline ([StatusProviderTimeout]), and
// returns the sections in priority then title order, redacted with
// the root status's patterns. The running instance calls it to answer
// `service status`; a nil r has none.
func ServiceStatusSections(ctx context.Context, r *Root) []StatusSection {
	if r == nil {
		return nil
	}
	r.statusProvidersMu.Lock()
	providers := make(map[string]StatusProvider, len(r.serviceStatusProviders))
	for k, v := range r.serviceStatusProviders {
		providers[k] = v
	}
	r.statusProvidersMu.Unlock()

	names := make([]string, 0, len(providers))
	for k := range providers {
		names = append(names, k)
	}
	sort.Strings(names)
	sections := make([]StatusSection, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sections[i] = runProvider(ctx, name, providers[name])
		}()
	}
	wg.Wait()
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Priority != sections[j].Priority {
			return sections[i].Priority < sections[j].Priority
		}
		return sections[i].Title < sections[j].Title
	})
	patterns := append(append([]string{}, defaultRedactPatterns...), r.statusConfig.RedactPatterns...)
	for i := range sections {
		sections[i].Data = redactSection(sections[i], patterns)
	}
	return sections
}
