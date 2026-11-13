package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// AnnotationSecretOutput is the cobra annotation that marks part of a
// command's output secret, the output-side twin of
// [AnnotationSecretFlag]. Records the bridge keeps (audit sinks, the
// idempotency ledger, the result cache) never carry the value of a
// field it names; the caller's own response is untouched. Set it with
// [MarkOutputSecret].
const AnnotationSecretOutput = cmdmeta.KeySecretOutput

// MarkOutputSecret declares which part of cmd's output is secret.
//
// With field names, each is a key of the command's structured output
// (the property names its output schema declares, see
// [hop.top/kit/go/console/cli.SetOutputSchema]); the value under that
// key is withheld from every record, wherever the key appears in the
// result, nested objects and lists included. The output schema may be
// declared before or after this call; a name it does not declare makes
// the command non-invocable on every served surface
// (cmdreflect.ReasonMalformedSchema) and is refused by Root.Validate,
// so a typo is never a silent gap. When the schema is already declared,
// the same check fails here and the annotation is left as it was.
//
// With no field names the whole output is secret, for a command that
// prints the value as text.
//
// Either way a record keeps no stdout or stderr of the command: text
// cannot be parsed for the value, so none of it is kept.
func MarkOutputSecret(cmd *cobra.Command, fields ...string) error {
	if cmd == nil {
		return fmt.Errorf("MarkOutputSecret: nil command")
	}
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			return fmt.Errorf("MarkOutputSecret: empty field name")
		}
	}
	fields = slices.Compact(slices.Sorted(slices.Values(fields)))
	prev, had := cmd.Annotations[AnnotationSecretOutput]
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[AnnotationSecretOutput] = cmdmeta.EncodeSecretOutput(fields...)
	if err := cmdmeta.CheckSecretOutput(cmd); err != nil {
		if had {
			cmd.Annotations[AnnotationSecretOutput] = prev
		} else {
			delete(cmd.Annotations, AnnotationSecretOutput)
		}
		return fmt.Errorf("MarkOutputSecret: %w", err)
	}
	return nil
}

// secretOutput is a leaf's resolved secret-output declaration.
type secretOutput struct {
	// all marks the whole output secret.
	all bool
	// fields are the output keys whose values are secret.
	fields map[string]bool
}

// secretOutputOf resolves a leaf's declaration from its descriptor:
// nil when it declares none.
func secretOutputOf(l *Leaf) *secretOutput {
	if l == nil || l.Descriptor == nil {
		return nil
	}
	o := l.Descriptor.Output
	if !o.SecretAll && len(o.SecretFields) == 0 {
		return nil
	}
	s := &secretOutput{all: o.SecretAll, fields: make(map[string]bool, len(o.SecretFields))}
	for _, f := range o.SecretFields {
		s.fields[f] = true
	}
	return s
}

// field reports whether key is a declared secret output field.
func (s *secretOutput) field(key string) bool { return s != nil && s.fields[key] }

// SecretOutput reports whether the command declares secret output:
// all of it, or the named fields. Hosts read it to explain what their
// records withhold; the bridge enforces it.
func (l *Leaf) SecretOutput() (all bool, fields []string) {
	s := l.secretOut
	if s == nil {
		return false, nil
	}
	for f := range s.fields {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	return s.all, fields
}

// settle is slot 13's idempotency half for an admitted call. A leaf
// declaring secret output is never recorded: the ledger would keep the
// answer, value included, for a replay, and a record the command does
// not control is what the declaration forbids. Its key is released
// instead, so a retry under the same key runs again.
func (a *Admission) settle(ctx context.Context, res Result, err error) {
	if a.leaf != nil && a.leaf.secretOut != nil {
		a.idem.abandon()
		return
	}
	a.idem.settle(ctx, res, err)
}

// cronView is the Result a cron job's result callback receives. A
// scheduled run has no caller who asked for the value, so the callback
// is a record, not a response: for a leaf declaring secret output it
// gets the redacted form audit sinks get, and every other leaf's
// Result unchanged.
func (b *Bridge) cronView(path []string, res Result) Result {
	leaf, err := b.resolveLeaf(path)
	if err != nil || leaf.secretOut == nil {
		return res
	}
	ctx := b.auditContext(context.Background(), leaf)
	_, red, _ := redactForAudit(ctx, Invocation{Path: path}, res, nil, true)
	red.Truncated = res.Truncated
	return red
}

// auditErr is the error a record of a run carries. Only the record's
// copy changes; the caller is returned the error as it was. A command
// that declares secret output may quote the value in the error it
// fails with, text nothing can parse for it, so the record keeps the
// message only of the errors the toolkit itself raises around the run
// (a deadline, an overload, a cancellation) and, for any other, a
// marker that still answers errors.Is for the original.
func (a *Admission) auditErr(err error) error {
	if err == nil || a.leaf == nil || a.leaf.secretOut == nil {
		return err
	}
	if errors.Is(err, ErrDeadlineExceeded) || errors.Is(err, ErrOverloaded) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &auditError{msg: auditRedacted + " (the command declares secret output)", cause: err}
}
