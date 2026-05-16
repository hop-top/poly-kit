package cli

import (
	"bytes"
	"errors"
	"io"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/idemstore"
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
		key := flag.Value.String()
		if key == "" {
			return orig(cmd, args)
		}

		ctx := cmd.Context()
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
		defer cmd.SetOut(origOut)

		err := orig(cmd, args)
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
