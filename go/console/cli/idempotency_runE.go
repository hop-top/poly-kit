package cli

import (
	"bytes"
	"errors"
	"io"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/cmdsurface"
)

// idempotencyKeyFlag is the auto-registered flag name. Adopters MUST
// NOT override this on conditional-idempotent commands; doing so
// shadows the kit-managed replay middleware.
const idempotencyKeyFlag = "idempotency-key"

// idempotencyAutoRegisteredAnnotation marks a command on which kit
// has auto-registered --idempotency-key; used to make the install
// step idempotent.
const idempotencyAutoRegisteredAnnotation = "kit.cli.idempotency.autoflag"

// installIdempotencyKeyFlag walks cmd's subtree and registers the
// kit-managed --idempotency-key=<key> flag on every leaf where the
// idempotency tag is "conditional" AND the side-effect tag is one
// of {write, destructive}. Read commands don't need the flag (they
// are already idempotent by spec); interactive commands are not a
// fit for replay.
//
// Idempotent: re-walking the same subtree never installs the flag
// twice.
func installIdempotencyKeyFlag(cmd *cobra.Command) {
	walk(cmd, func(c *cobra.Command) {
		if !isLeaf(c) || isBuiltin(c) || !c.Runnable() {
			return
		}
		i, ok := GetIdempotency(c)
		if !ok || i != IdempotencyConditional {
			return
		}
		s, ok := GetSideEffect(c)
		if !ok || (!isWriteLike(s) && !isDestructiveLike(s)) {
			return
		}
		if c.Annotations[idempotencyAutoRegisteredAnnotation] == "true" {
			return
		}
		c.Flags().String(idempotencyKeyFlag, "",
			"Idempotency key for replay. Same key + same tool replays the recorded output.")
		if c.Annotations == nil {
			c.Annotations = make(map[string]string)
		}
		c.Annotations[idempotencyAutoRegisteredAnnotation] = "true"
	})
}

// wrapIdempotencyRunE wraps orig such that, when the caller passes a
// non-empty --idempotency-key, the middleware:
//
//  1. Looks up the key in store; on hit, writes the recorded output
//     to cmd's stdout, sets the recorded exit code on cmd's context,
//     and returns nil. The orig RunE is skipped entirely.
//  2. On miss, runs orig with stdout teed into a buffer. After orig
//     completes (success or error), records the captured output
//     under the key and returns orig's error.
//
// When --idempotency-key is empty or the flag isn't registered on
// the command, orig runs unchanged.
//
// The store key is the flag's value on the command line. Under a
// served invocation it is that value confined to the caller
// ([cmdsurface.ScopeIdempotencyKey]): the invocation's established
// principal and tenant, else its surface, claimed caller and client
// host. A caller who sends another caller's key is not answered with
// that caller's recorded output, and served and local keys never
// meet.
//
// store must be non-nil; callers (Root.WrapRunE) supply r.IdemStore
// or skip the wrap when it's nil.
func wrapIdempotencyRunE(
	orig func(*cobra.Command, []string) error,
	store idemstore.Store,
) func(*cobra.Command, []string) error {
	if orig == nil || store == nil {
		return orig
	}
	return func(cmd *cobra.Command, args []string) error {
		flag := cmd.Flags().Lookup(idempotencyKeyFlag)
		if flag == nil {
			return orig(cmd, args)
		}
		ctx := cmd.Context()
		// A served invocation's key is confined to its caller; a
		// local user's key is used as typed.
		key := cmdsurface.ScopeIdempotencyKey(ctx, flag.Value.String())
		if key == "" {
			return orig(cmd, args)
		}

		if r, hit, err := store.Lookup(ctx, key); err == nil && hit {
			_, _ = cmd.OutOrStdout().Write(r.Output)
			return nil
		} else if err != nil {
			// Lookup failed. Fall through to running orig — we'd
			// rather over-execute than refuse a request because the
			// idempotency cache is unhealthy. Tests that need to
			// observe the failure can pass a store whose Lookup
			// returns the canonical sentinel.
			_ = err
		}

		// Capture stdout while orig runs. The wrap is best-effort:
		// if cmd.OutOrStdout() returns os.Stdout we install a tee;
		// otherwise we wrap whatever writer is there.
		buf := &bytes.Buffer{}
		origOut := cmd.OutOrStdout()
		cmd.SetOut(io.MultiWriter(origOut, buf))
		defer restoreOut(cmd, origOut)

		err := orig(cmd, args)
		// Skip recording when a flag-validator rejected the call
		// before adopter dispatch — there's no useful output to
		// replay, and caching the rejection would poison subsequent
		// runs that pass the same key with a valid flag value.
		if errors.Is(err, errFlagValidation) {
			return err
		}
		exit := 0
		if err != nil {
			exit = exitCodeFor(err)
		}
		_ = store.Record(ctx, key, idemstore.Result{
			Key:      key,
			ExitCode: exit,
			Output:   buf.Bytes(),
		})
		return err
	}
}

// restoreOut puts back the writer cmd wrote to before the capture.
// A command that inherited its writer from an ancestor inherits it
// again, rather than keeping the resolved writer as its own: a served
// runner points the root at each invocation's buffer, and a leaf
// pinned to one invocation's buffer would swallow every later
// invocation's output.
func restoreOut(cmd *cobra.Command, orig io.Writer) {
	cmd.SetOut(nil)
	if !sameWriter(cmd.OutOrStdout(), orig) {
		cmd.SetOut(orig)
	}
}

// sameWriter reports whether a and b are the same writer. Writers
// whose dynamic type is not comparable are never the same.
func sameWriter(a, b io.Writer) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// exitCodeFor extracts a useful exit code from err. Unwraps to find
// an output.Error; defaults to 1 for unstructured failures.
func exitCodeFor(err error) int {
	var ce asCLIError
	if errors.As(err, &ce) {
		if out := ce.AsCLIError(); out != nil && out.ExitCode != 0 {
			return out.ExitCode
		}
	}
	return 1
}
