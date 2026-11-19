// Package ref resolves secret references embedded in configuration values.
//
// A value is either a literal, returned unchanged, or a reference:
//
//	op://<vault>/<item>/<field>[?account=<name>]   1Password, read with `op read`
//	secret:NAME                                    a caller-supplied store lookup
//
// Resolution fails closed. An unresolvable reference returns an error that
// wraps ErrUnresolved; the literal reference and an empty value are never
// returned in its place. Errors name the reference kind (and, via ResolveMap,
// the map key) and never carry a resolved value.
package ref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	// OnePasswordPrefix introduces a 1Password secret reference.
	OnePasswordPrefix = "op://"
	// NamePrefix introduces a reference to a named secret in a caller store.
	NamePrefix = "secret:"

	opReadTimeout = 30 * time.Second
	maxDiagLen    = 200
)

// ErrUnresolved is wrapped by every resolution failure.
var ErrUnresolved = errors.New("secret reference could not be resolved")

// Lookup reports the value bound to name. found=false means the store has no
// such secret; a non-nil err means the store could not be read.
type Lookup func(ctx context.Context, name string) (value string, found bool, err error)

// OpReader reads one op:// reference. account is empty when the reference
// carries no ?account= selector.
type OpReader func(ctx context.Context, ref, account string) (string, error)

// Opener supplies the backends a resolution may use.
type Opener struct {
	// Lookup serves secret:NAME. Nil means no store is available.
	Lookup Lookup
	// OpRead serves op://. Nil means ExecOpRead (the `op` CLI on PATH).
	OpRead OpReader
}

// IsRef reports whether value is a reference. A bare prefix names nothing and
// is a literal.
func IsRef(value string) bool {
	v := strings.TrimSpace(value)
	if rest, ok := strings.CutPrefix(v, OnePasswordPrefix); ok {
		return rest != ""
	}
	if rest, ok := strings.CutPrefix(v, NamePrefix); ok {
		return strings.TrimSpace(rest) != ""
	}
	return false
}

// Resolve returns value unchanged when it is a literal, or the secret it
// references. On failure it returns "" and an error wrapping ErrUnresolved.
func Resolve(ctx context.Context, value string, o Opener) (string, error) {
	if !IsRef(value) {
		return value, nil
	}
	v := strings.TrimSpace(value)

	var (
		got  string
		kind string
		err  error
	)
	if strings.HasPrefix(v, OnePasswordPrefix) {
		kind = OnePasswordPrefix
		got, err = o.readOp(ctx, v)
	} else {
		kind = NamePrefix
		got, err = o.readName(ctx, strings.TrimSpace(strings.TrimPrefix(v, NamePrefix)))
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s reference: %w", ErrUnresolved, kind, err)
	}
	got = strings.TrimSpace(got)
	if got == "" {
		return "", fmt.Errorf("%w: %s reference resolved to an empty value", ErrUnresolved, kind)
	}
	if IsRef(got) {
		// Never hand a reference to a consumer as if it were a secret.
		return "", fmt.Errorf("%w: %s reference resolved to another reference; chained references are not supported", ErrUnresolved, kind)
	}
	return got, nil
}

// ResolveMap resolves every value of in and returns a new map; in is not
// modified. The first failure, in sorted key order, returns a nil map and an
// error naming the key (never a value), so no partial or literal-reference
// environment escapes.
func ResolveMap(ctx context.Context, in map[string]string, o Opener) (map[string]string, error) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(in))
	for _, k := range keys {
		v, err := Resolve(ctx, in[k], o)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func (o Opener) readName(ctx context.Context, name string) (string, error) {
	if o.Lookup == nil {
		return "", errors.New("no secret store available")
	}
	v, found, err := o.Lookup(ctx, name)
	if err != nil {
		return "", fmt.Errorf("store unavailable: %w", err)
	}
	if !found {
		return "", errors.New("not found in the secret store")
	}
	return v, nil
}

func (o Opener) readOp(ctx context.Context, value string) (string, error) {
	read := o.OpRead
	if read == nil {
		read = ExecOpRead
	}
	r, account := splitAccount(value)
	ctx, cancel := context.WithTimeout(ctx, opReadTimeout)
	defer cancel()
	return read(ctx, r, account)
}

// splitAccount separates the ?account=<name> selector from an op:// reference.
// op has no account syntax inside the reference; the account goes to
// `op read --account`. Other query parameters (e.g. ?attribute=otp) belong to
// op and are kept.
func splitAccount(value string) (string, string) {
	base, query, found := strings.Cut(value, "?")
	if !found {
		return value, ""
	}
	vals, err := url.ParseQuery(query)
	if err != nil || vals.Get("account") == "" {
		return value, ""
	}
	account := vals.Get("account")
	vals.Del("account")
	if rest := vals.Encode(); rest != "" {
		base += "?" + rest
	}
	return base, account
}

// ExecOpRead is the default OpReader: `op read --no-newline [--account A] REF`.
// Failure text is op's own diagnostic, truncated; op prints no value on error.
func ExecOpRead(ctx context.Context, ref, account string) (string, error) {
	args := []string{"read", "--no-newline"}
	if account != "" {
		args = append(args, "--account", account)
	}
	args = append(args, ref)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "op", args...) //nolint:gosec // fixed binary; ref comes from the caller's own config
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("op CLI not found on PATH")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			if len(msg) > maxDiagLen {
				msg = msg[:maxDiagLen]
			}
			return "", fmt.Errorf("op read failed: %s", msg)
		}
		return "", fmt.Errorf("op read failed: %w", err)
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}
