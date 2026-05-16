package verbs

import (
	"context"
	"fmt"

	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// idempotency_replay_clean: { } — operates on the on-step's
// cassette dir AND a paired "replay" cassette dir. The adopter is
// responsible for arranging that the on-step's CassetteDir holds
// the apply run and there exists a sibling step capture (named
// "<on>_replay" or with explicit metadata) holding the replay run.
//
// v1 convention: this verb expects OtherCaptures to contain an
// entry keyed "<on>__replay". If absent the verb surfaces
// ungradable so the adopter knows to record paired runs.

func init() {
	register(&Entry{
		Kind:     KindIdempotencyReplayClean,
		Validate: nil,
		Evaluate: evalIdempotencyReplayClean,
	})
}

func evalIdempotencyReplayClean(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	replayKey := spec.On + "__replay"
	replay, ok := vctx.OtherCaptures[replayKey]
	if !ok {
		return Ungradable(fmt.Sprintf("idempotency_replay_clean: no replay capture %q recorded; record apply+replay with the second step id suffixed __replay", replayKey))
	}
	d, err := diff.Cassettes(vctx.Capture.CassetteDir, replay.CassetteDir)
	if err != nil {
		return Ungradable("idempotency_replay_clean: " + err.Error())
	}
	if d.Empty() {
		return EvalResult{Status: StatusPass, Expected: "replay clean"}
	}
	return Fail(d.Format(nil), "replay clean", fmt.Sprintf("%d cassette diff entries between apply and replay", len(d.Entries)))
}
