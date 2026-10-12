package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
)

// Service status sections.
//
// `service status` reports kit's fixed rows — each service the
// running instance started, its readiness, address and pid — and,
// below them, sections the tool contributes about the same instance:
// queue depths, the profile it serves, per-channel health. They are
// [StatusProvider]s, the type the root `status` runs, registered with
// [Root.RegisterServiceStatusProvider] and run by the instance itself,
// in the `service start` process, when a client asks: what they read
// is the running instance's state, not the client's.

// serviceStatusOutput is what `service status` and `service restart`
// report.
type serviceStatusOutput struct {
	// Services are kit's rows, one per service the instance started.
	Services []serviceStatusRow `json:"services" yaml:"services"`
	// Sections are the tool's, in priority then title order.
	Sections []StatusSection `json:"sections" yaml:"sections"`
}

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

// serviceStatusSections runs every service status provider at once,
// each under the status pipeline's per-provider deadline, and returns
// the sections ordered and redacted.
func (r *Root) serviceStatusSections(ctx context.Context) []StatusSection {
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

// renderServiceStatus prints out in the active format. json and yaml
// carry the whole object; the tabular formats print kit's rows, then,
// when the tool has sections, a blank line and one row per section in
// the root status's SECTION/STATUS/DETAIL projection.
func renderServiceStatus(cmd *cobra.Command, root *Root, out serviceStatusOutput) error {
	if out.Services == nil {
		out.Services = []serviceStatusRow{}
	}
	if out.Sections == nil {
		out.Sections = []StatusSection{}
	}
	format := activeOutputFormat(cmd, root.Viper)
	if !isStatusTabular(format) {
		return output.Dispatch(cmd, root.Viper, out)
	}
	if err := output.Dispatch(cmd, root.Viper, out.Services); err != nil {
		return err
	}
	if len(out.Sections) == 0 {
		return nil
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return output.Render(w, format, statusRows(out.Sections))
}
