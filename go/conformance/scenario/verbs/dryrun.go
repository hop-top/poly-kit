package verbs

import (
	"context"
	"fmt"

	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// dry_run_no_mutation: { } — operates on the on-step's cassette.
// Pass iff every recorded interaction classifies as Read.

func init() {
	register(&Entry{
		Kind:     KindDryRunNoMutation,
		Validate: nil,
		Evaluate: evalDryRunNoMutation,
	})
}

func evalDryRunNoMutation(_ context.Context, _ AssertionSpec, vctx VerbContext) EvalResult {
	items, err := diff.List(vctx.Capture.CassetteDir)
	if err != nil {
		return Ungradable("dry_run_no_mutation: " + err.Error())
	}
	for _, it := range items {
		cls := classifyPayload(it.Adapter, it.ReqPayload)
		if cls != classifier.ClassRead {
			return Fail(fmt.Sprintf("%s %s (%s)", it.Adapter, it.Summary, cls),
				"all Read", fmt.Sprintf("non-Read interaction recorded under dry-run: %s %s", it.Adapter, it.Summary))
		}
	}
	return EvalResult{Status: StatusPass, Expected: "all Read"}
}
