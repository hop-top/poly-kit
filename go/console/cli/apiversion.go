package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
)

// parseSchemaVersion splits "MAJOR.MINOR" into integers. Strict format
// — anything else returns ok=false and the caller decides what to do.
func parseSchemaVersion(s string) (major, minor int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// schemaVersionLessOrEqual reports whether a <= b, comparing
// MAJOR.MINOR component-wise. Unparseable versions sort to "highest"
// (treating an unset/garbled value as "always present"). Used by both
// the kit/since filter ("since <= requested?") and the kit/min-api
// check ("min <= requested?").
func schemaVersionLessOrEqual(a, b string) bool {
	aMaj, aMin, okA := parseSchemaVersion(a)
	bMaj, bMin, okB := parseSchemaVersion(b)
	if !okA {
		// Unparseable a (e.g. unset annotation): treat as "always
		// available" so it never gets filtered. a <= b == true.
		return true
	}
	if !okB {
		// Unparseable b: be conservative and don't filter.
		return true
	}
	if aMaj != bMaj {
		return aMaj < bMaj
	}
	return aMin <= bMin
}

// flagSinceMap parses the kit/flag-since annotation into a map of
// flag name → MAJOR.MINOR version. Format:
// "flag1=1.0,flag2=1.2".
func flagSinceMap(cmd *cobra.Command) map[string]string {
	if cmd == nil || cmd.Annotations == nil {
		return nil
	}
	raw := cmd.Annotations[kitFlagSince]
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		eq := strings.Index(entry, "=")
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(entry[:eq])
		ver := strings.TrimSpace(entry[eq+1:])
		if name != "" && ver != "" {
			out[name] = ver
		}
	}
	return out
}

// applyAPIVersionFilter walks cmd's subtree and:
//
//  1. Hides any leaf whose kit/since is newer than requested. Hidden
//     commands are also disabled (Hidden=true + RunE returns
//     NotFound) so adopters who bypass help printing still get a
//     uniform refusal.
//  2. Walks present-and-visible leaves and refuses any flag whose
//     kit/flag-since entry is newer than requested. The refusal is
//     installed on the leaf as a PreRunE wrapper; it fires AFTER
//     cobra has parsed flags so we can inspect Changed().
//
// requested is the user-supplied --api-version. Empty means
// "negotiation off"; the function is a no-op.
func applyAPIVersionFilter(root *cobra.Command, requested string) {
	if requested == "" {
		return
	}
	walk(root, func(cmd *cobra.Command) {
		if cmd == root {
			return
		}
		// Hide commands whose kit/since is newer than requested.
		if cmd.Annotations != nil {
			if since := cmd.Annotations[kitSince]; since != "" {
				// Newer than requested? since > requested → hide.
				if !schemaVersionLessOrEqual(since, requested) {
					cmd.Hidden = true
					installAPIVersionRefuse(cmd, since, requested)
					return
				}
			}
		}
		// Refuse flags introduced after requested. Installed even on
		// non-leaf groups so flag-handling stays uniform.
		if isLeaf(cmd) && !isBuiltin(cmd) && cmd.Runnable() {
			installFlagSinceGuard(cmd, requested)
		}
	})
}

// installAPIVersionRefuse wraps cmd's RunE so it returns an
// UNSUPPORTED_API_VERSION error if anyone manages to reach it despite
// being hidden.
func installAPIVersionRefuse(cmd *cobra.Command, since, requested string) {
	msg := fmt.Sprintf(
		"%s requires --api-version >= %s; requested %s",
		cmd.CommandPath(), since, requested)
	cmd.RunE = func(*cobra.Command, []string) error {
		return &output.Error{
			Code:     CodeUnsupportedAPIVersion,
			Message:  msg,
			ExitCode: 2,
		}
	}
	cmd.Run = nil
}

// installFlagSinceGuard installs a PersistentPreRunE on cmd that
// rejects any flag whose kit/flag-since entry is newer than
// requested AND was actually changed by the caller. Untouched
// newer flags pass silently (so commands compatible across versions
// just don't trip on optional new flags).
func installFlagSinceGuard(cmd *cobra.Command, requested string) {
	since := flagSinceMap(cmd)
	if len(since) == 0 {
		return
	}
	prev := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		for name, ver := range since {
			f := c.Flags().Lookup(name)
			if f == nil {
				continue
			}
			if !f.Changed {
				continue
			}
			// flag-since > requested ⇒ refuse.
			if !schemaVersionLessOrEqual(ver, requested) {
				return &output.Error{
					Code: CodeUnsupportedAPIVersion,
					Message: fmt.Sprintf(
						"--%s requires --api-version >= %s; requested %s",
						name, ver, requested),
					ExitCode: 2,
				}
			}
		}
		if prev != nil {
			return prev(c, args)
		}
		return nil
	}
}

// checkMinAPIVersion enforces kit/min-api-version on the root. Returns
// a non-nil *output.Error when the requested version is below min;
// nil when allowed (or when min is unset).
func checkMinAPIVersion(root *cobra.Command, requested string) *output.Error {
	if root == nil || requested == "" || root.Annotations == nil {
		return nil
	}
	min := root.Annotations[kitMinAPIVersion]
	if min == "" {
		return nil
	}
	if schemaVersionLessOrEqual(min, requested) {
		return nil
	}
	return &output.Error{
		Code: CodeUnsupportedAPIVersion,
		Message: fmt.Sprintf(
			"--api-version %s is below the tool's minimum supported (%s)",
			requested, min),
		ExitCode: 2,
	}
}

// SetSinceVersion attaches the kit/since annotation in idiomatic form.
// Adopters call this on commands they introduce after MAJOR.0 to opt
// into capability-negotiation filtering. Untagged commands are
// "always present".
func SetSinceVersion(cmd *cobra.Command, ver string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitSince] = ver
}

// SetFlagSince attaches a kit/flag-since entry for one flag. Multiple
// calls accumulate into a comma-separated annotation value.
func SetFlagSince(cmd *cobra.Command, flag, ver string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	existing := cmd.Annotations[kitFlagSince]
	if existing == "" {
		cmd.Annotations[kitFlagSince] = flag + "=" + ver
		return
	}
	cmd.Annotations[kitFlagSince] = existing + "," + flag + "=" + ver
}

// SetMinAPIVersion stamps the root with its minimum supported API
// version. Lower --api-version values are refused at parse time.
func SetMinAPIVersion(root *cobra.Command, ver string) {
	if root.Annotations == nil {
		root.Annotations = make(map[string]string)
	}
	root.Annotations[kitMinAPIVersion] = ver
}

// ApplyAPIVersionFilter is the public shim around the internal
// applyAPIVersionFilter. Adopters who manage their own dispatch (e.g.
// tests bypassing fang.Execute) can invoke this after registering
// commands to apply capability-mode filtering identical to what
// Execute does. requested is the user-supplied --api-version
// (MAJOR.MINOR); empty disables filtering. Calling this multiple times
// is safe — Hidden flags and PreRunE wrappers are idempotent w.r.t.
// the same requested version.
func (r *Root) ApplyAPIVersionFilter(requested string) {
	if r == nil || r.Cmd == nil {
		return
	}
	applyAPIVersionFilter(r.Cmd, requested)
}

// CheckMinAPIVersion is the public shim around checkMinAPIVersion. Used
// by adopters/tests to enforce kit/min-api-version against a
// user-supplied --api-version without going through Execute.
func (r *Root) CheckMinAPIVersion(requested string) error {
	if e := checkMinAPIVersion(r.Cmd, requested); e != nil {
		return e
	}
	return nil
}

// scanArgsForAPIVersion peeks at the resolved args (SetArgs override
// or os.Args) for --api-version=<v> or --api-version <v>. Returns "" if
// not present. Pre-parse so we can apply filters before cobra dispatch.
func (r *Root) scanArgsForAPIVersion() string {
	args := r.resolveArgs()
	for i, a := range args {
		if a == "--"+apiVersionFlag {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(a, "--"+apiVersionFlag+"=") {
			return strings.TrimPrefix(a, "--"+apiVersionFlag+"=")
		}
	}
	return ""
}

// SetDeprecatedSince attaches kit/deprecated-since.
func SetDeprecatedSince(cmd *cobra.Command, ver string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitDeprecatedSince] = ver
}

// SetRemovalTarget attaches kit/removal-target.
func SetRemovalTarget(cmd *cobra.Command, ver string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitRemovalTarget] = ver
}
