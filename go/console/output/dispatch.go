package output

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// stdoutSentinel is the value of --output that means "write to stdout"
// even when the flag is set explicitly. Empty string also resolves to
// stdout (default behavior).
const stdoutSentinel = "-"

// Dispatch resolves the active output flags from v and renders data,
// honoring --format, --format-opt, --cols/--columns, --template, and
// --output (-o) per the rules wired by RegisterFlagsWith.
//
// Resolution order:
//  1. Resolve writer: empty or "-" output → cmd.OutOrStdout(); else open
//     file (T-0989).
//  2. --format-help short-circuit: when set, render the formatter
//     catalog (or one formatter's options when --format is also set)
//     and return without consuming data.
//  3. Resolve format: explicit --format wins; else --output extension
//     mapped via the active registry; else default ("table").
//  4. Mismatch detection: explicit --format combined with an --output
//     extension that maps to a different formatter is a hard error.
//  5. --template path: build {.Items, .Cols} input, run text/template,
//     return.
//  6. Else: ParseOptions, validate cols against TableHeaders(data), call
//     Formatter.Render.
//
// Dispatch never closes cmd.OutOrStdout(); it only closes file writers it
// opened.
func Dispatch(cmd *cobra.Command, v *viper.Viper, data any) error {
	registry := registryFor(cmd)

	// 1. Writer + path.
	w, closer, err := resolveWriter(cmd, v)
	if err != nil {
		return err
	}
	if closer != nil {
		defer closer()
	}

	// 2. --format-help short-circuit: honor before format resolution so
	// callers can inspect the catalog without supplying valid data.
	if isFormatHelpSet(cmd, v) {
		// When --format is also set (and changed), scope to one formatter.
		key := ""
		if pf := cmd.Flags().Lookup(flagFormat); pf != nil && pf.Changed {
			key = pf.Value.String()
		}
		return RenderFormatHelp(w, registry, key)
	}

	// 3 + 4. Format resolution.
	format, err := resolveFormat(cmd, v, registry)
	if err != nil {
		return err
	}

	// 4. Template escape hatch.
	tmplSrc, _ := lookupStringFlag(cmd, v, flagTemplate)
	cols := resolveCols(cmd, v)
	if tmplSrc != "" {
		if len(cols) > 0 {
			return fmt.Errorf("--template and --cols are mutually exclusive")
		}
		return renderTemplate(w, tmplSrc, data)
	}

	// 5. Formatter render.
	formatter, ok := registry.Lookup(format)
	if !ok {
		return fmt.Errorf("unknown output format %q (valid: %s)",
			format, strings.Join(registry.Keys(), ", "))
	}

	opts, err := ParseOptions(lookupStringSlice(cmd, v, flagFormatOpt), formatter.Options())
	if err != nil {
		return err
	}

	if len(cols) > 0 {
		if err := validateCols(data, cols); err != nil {
			return err
		}
	}

	return formatter.Render(w, data, opts, cols)
}

// resolveWriter returns the io.Writer Dispatch should render to and an
// optional close func. closer is nil for stdout-bound writers.
func resolveWriter(cmd *cobra.Command, v *viper.Viper) (io.Writer, func(), error) {
	path, _ := lookupStringFlag(cmd, v, flagOutput)
	if path == "" || path == stdoutSentinel {
		return cmd.OutOrStdout(), nil, nil
	}
	return openOutputFile(path)
}

// isFormatHelpSet reports whether --format-help was set on the command
// line or via the supplied viper. Mirrors lookupStringFlag's preference
// for the cobra flag's `Changed` state, falling back to viper for
// programmatic callers (tests, configs).
func isFormatHelpSet(cmd *cobra.Command, v *viper.Viper) bool {
	if pf := commandFlag(cmd, flagFormatHelp); pf != nil && pf.Changed {
		return pf.Value.String() == "true"
	}
	if v != nil {
		return v.GetBool(flagFormatHelp)
	}
	return false
}

// lookupStringSlice mirrors lookupStringFlag for repeatable
// StringSlice flags (--format-opt, --cols, --columns). When the cobra
// flag is `Changed` we prefer it; otherwise we fall back to viper.
func lookupStringSlice(cmd *cobra.Command, v *viper.Viper, name string) []string {
	if pf := commandFlag(cmd, name); pf != nil && pf.Changed {
		if sliceVal, ok := pf.Value.(interface{ GetSlice() []string }); ok {
			return sliceVal.GetSlice()
		}
		// pflag's stringSliceValue exposes the parsed slice via String();
		// fall back to splitting if the assertion above missed.
		if s := pf.Value.String(); s != "" {
			return strings.Split(strings.Trim(s, "[]"), ",")
		}
	}
	if v != nil {
		return v.GetStringSlice(name)
	}
	return nil
}

// lookupStringFlag returns the value of name plus whether the user
// explicitly set it on the command line. Resolution order:
//  1. cobra flag set on cmd or any ancestor (including inherited
//     persistent flags); used when the flag is `Changed`.
//  2. viper value bound to name (config files, env, viper.Set).
//  3. cobra default value for the flag (used as a last resort so
//     callers that pass an unrelated viper still see the registered
//     default — e.g. --format defaulting to "table").
//
// `explicit` is true only when the flag was changed on the command
// line, mirroring pflag.Changed semantics.
func lookupStringFlag(cmd *cobra.Command, v *viper.Viper, name string) (value string, explicit bool) {
	if pf := commandFlag(cmd, name); pf != nil {
		if pf.Changed {
			return pf.Value.String(), true
		}
		if v != nil {
			if s := v.GetString(name); s != "" {
				return s, false
			}
		}
		return pf.DefValue, false
	}
	if v != nil {
		return v.GetString(name), false
	}
	return "", false
}

// commandFlag returns the *pflag.Flag for name, walking persistent
// flags on cmd and its ancestors before falling back to cmd.Flags().
// We prefer PersistentFlags() because cobra lazily merges persistent
// flags into the local set, which can lose `Changed` state across the
// boundary depending on parse timing.
func commandFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for c := cmd; c != nil; c = c.Parent() {
		if pf := c.PersistentFlags().Lookup(name); pf != nil {
			return pf
		}
	}
	if pf := cmd.Flags().Lookup(name); pf != nil {
		return pf
	}
	return nil
}

// openOutputFile opens path with O_WRONLY|O_CREATE|O_TRUNC (T-0989). It
// returns a clear error when path resolves to a directory rather than a
// regular file.
func openOutputFile(path string) (io.Writer, func(), error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, nil, fmt.Errorf("output path %q is a directory", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open output %q: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// resolveFormat picks the active formatter key based on --format and
// --output extension. Explicit --format wins; default --format paired
// with a known --output extension switches to that formatter; explicit
// --format paired with a different extension is a hard mismatch error.
func resolveFormat(cmd *cobra.Command, v *viper.Viper, registry *Registry) (string, error) {
	format, explicit := lookupStringFlag(cmd, v, flagFormat)
	if format == "" {
		format = Table
	}

	path, _ := lookupStringFlag(cmd, v, flagOutput)
	if path == "" || path == stdoutSentinel {
		return format, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return format, nil
	}
	mapped, known := registry.ExtensionMap()[ext]
	if !known {
		return format, nil
	}

	if !explicit {
		return mapped, nil
	}
	if mapped != format {
		return "", fmt.Errorf(
			"format %q does not match output extension %q (use -o file.%s or --format %s)",
			format, ext, strings.TrimPrefix(strings.ToLower(formatPrimaryExt(registry, format)), "."), mapped,
		)
	}
	return format, nil
}

// formatPrimaryExt returns the first extension declared by the formatter
// for use in error hints. Falls back to "."+key when no extensions exist.
func formatPrimaryExt(r *Registry, key string) string {
	f, ok := r.Lookup(key)
	if !ok {
		return "." + key
	}
	exts := f.Extensions()
	if len(exts) == 0 {
		return "." + key
	}
	return exts[0]
}

// validateCols ensures every name in cols matches a `table:""` tag header
// on data's element type. Mirrors the error format used by filterColumns
// so json/yaml see the same diagnostic table renders see.
func validateCols(data any, cols []string) error {
	headers := TableHeaders(reflect.TypeOf(data))
	if len(headers) == 0 {
		// No tagged headers — nothing we can validate against. Defer to
		// the formatter (which may simply emit the full payload).
		return nil
	}
	have := make(map[string]struct{}, len(headers))
	for _, h := range headers {
		have[h] = struct{}{}
	}
	for _, c := range cols {
		if _, ok := have[c]; !ok {
			return fmt.Errorf("unknown column %q (valid: %s)",
				c, strings.Join(headers, ", "))
		}
	}
	return nil
}

// renderTemplate runs src against an input value of the form
// {Items: []map[string]any, Cols: []string}. Items projects all
// `table:""` fields; Cols lists every header in struct field order.
func renderTemplate(w io.Writer, src string, data any) error {
	tmpl, err := template.New("output").Parse(src)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	headers := TableHeaders(reflect.TypeOf(data))
	items := projectToMaps(data, nil)
	// projectToMaps may return data unchanged for non-struct inputs;
	// normalize to []map[string]any when possible so .Items is iterable.
	itemSlice, _ := items.([]map[string]any)
	if itemSlice == nil {
		if m, ok := items.(map[string]any); ok {
			itemSlice = []map[string]any{m}
		}
	}

	input := struct {
		Items []map[string]any
		Cols  []string
		Data  any
	}{
		Items: itemSlice,
		Cols:  headers,
		Data:  data,
	}
	if err := tmpl.Execute(w, input); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}
