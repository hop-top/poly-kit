package cmdreflect

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// reflectArgs parses the kit/args annotation. A trailing "?" marks
// an optional argument.
func reflectArgs(cmd *cobra.Command) []Arg {
	if cmd.Annotations == nil {
		return nil
	}
	raw := cmd.Annotations[annArgs]
	if raw == "" {
		return nil
	}
	names := splitCSV(raw)
	out := make([]Arg, 0, len(names))
	for _, n := range names {
		out = append(out, Arg{
			Name:     strings.TrimSuffix(n, "?"),
			Required: !strings.HasSuffix(n, "?"),
		})
	}
	return out
}

// reflectFlags projects a pflag set. Every flag is reflected,
// hidden ones included, with Hidden recorded — dropping them here
// would repeat the mistake this package exists to fix. Consumers
// that must not show hidden flags filter on the field.
func reflectFlags(set *pflag.FlagSet, since map[string]string) []Flag {
	if set == nil {
		return nil
	}
	var out []Flag
	set.VisitAll(func(f *pflag.Flag) {
		fl := Flag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			Description: f.Usage,
			Default:     f.DefValue,
			Hidden:      f.Hidden,
			Deprecated:  f.Deprecated != "",
		}
		if f.Annotations != nil {
			if v := f.Annotations[requiredFlagAnnotation]; len(v) > 0 && v[0] == "true" {
				fl.Required = true
			}
		}
		if v, ok := since[f.Name]; ok {
			fl.SinceVersion = v
		}
		out = append(out, fl)
	})
	return out
}

// flagSince parses the kit/flag-since annotation into a flag name →
// version map.
func flagSince(cmd *cobra.Command) map[string]string {
	out := map[string]string{}
	if cmd == nil || cmd.Annotations == nil {
		return out
	}
	raw := cmd.Annotations[annFlagSince]
	if raw == "" {
		return out
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		eq := strings.Index(entry, "=")
		if eq < 0 {
			continue
		}
		out[strings.TrimSpace(entry[:eq])] = strings.TrimSpace(entry[eq+1:])
	}
	return out
}

// splitCSV splits a comma-separated annotation value, trimming
// whitespace and dropping empty entries. Returns nil for the empty
// string so an absent annotation stays distinguishable from an
// explicitly empty list.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
