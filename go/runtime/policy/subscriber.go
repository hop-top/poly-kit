package policy

import (
	"context"
	"encoding/json"
	"fmt"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// Topics watched. Mirror the ADR.
const (
	topicStatePreTransitioned = "kit.runtime.state.pre_transitioned"
	topicEntityPreValidated   = "kit.runtime.entity.pre_validated"
	topicEntityPrePersisted   = "kit.runtime.entity.pre_persisted"
)

// Wire attaches the engine to b on the three veto-able topics. Returns
// a cancel func that unsubscribes all handlers.
//
// Three explicit Subscribes — kit/bus wildcard `*` matches a whole
// segment, not a substring (see bus/event.go), so `pre_*` would not
// expand to `pre_validated` / `pre_persisted`. Listing the topics
// explicitly is also clearer for ops grepping subscriptions.
func Wire(b bus.Bus, eng *Engine) func() {
	uState := b.Subscribe(topicStatePreTransitioned, eng.OnTransition)
	uVal := b.Subscribe(topicEntityPreValidated, eng.OnEntityChange)
	uPer := b.Subscribe(topicEntityPrePersisted, eng.OnEntityChange)
	return func() {
		uState()
		uVal()
		uPer()
	}
}

// OnTransition handles kit.runtime.state.pre_transitioned. It expects
// payload to be domain.TransitionPayload (or any value that JSON-
// marshals into {from,to,force,...}).
func (e *Engine) OnTransition(ctx context.Context, ev bus.Event) error {
	act, err := e.activation(ctx, ev)
	if err != nil {
		return fmt.Errorf("policy.OnTransition: %w", err)
	}
	return e.Decide(string(ev.Topic), act)
}

// OnEntityChange handles both pre_validated and pre_persisted topics.
// It expects payload to be domain.PreEntityPayload.
func (e *Engine) OnEntityChange(ctx context.Context, ev bus.Event) error {
	act, err := e.activation(ctx, ev)
	if err != nil {
		return fmt.Errorf("policy.OnEntityChange: %w", err)
	}
	return e.Decide(string(ev.Topic), act)
}

// activation builds the CEL input map from event + context.
func (e *Engine) activation(ctx context.Context, ev bus.Event) (map[string]any, error) {
	princ := e.princ(ctx)
	pload := payloadAsMap(ev.Payload)
	resource := resourceFromPayload(ev.Payload, pload)
	entity := entityFromPayload(ev.Payload, pload)
	cm := contextAttrsFromCtx(ctx)
	return map[string]any{
		"principal": map[string]any{
			"id":     princ.ID,
			"role":   princ.Role,
			"source": princ.Source,
		},
		"resource": resource,
		"entity":   entity,
		"context":  cm,
		"payload":  pload,
	}, nil
}

// entityFromPayload builds the `entity` CEL binding for stage-driven
// rules: {kind, op, track_type, ...}. Pulls from PreEntityPayload's Op
// and the validated/raw entity's fields when available; falls back to
// payload-as-map for non-domain payloads.
func entityFromPayload(raw any, m map[string]any) map[string]any {
	out := map[string]any{
		"kind":       "",
		"op":         "",
		"track_type": "",
	}
	if pe, ok := raw.(domain.PreEntityPayload); ok {
		out["op"] = string(pe.Op)
		if pe.Entity != nil {
			fields := payloadAsMap(pe.Entity)
			if k, ok := fields["Kind"].(string); ok {
				out["kind"] = k
			} else if k, ok := fields["kind"].(string); ok {
				out["kind"] = k
			}
			if tt, ok := fields["TrackType"].(string); ok {
				out["track_type"] = tt
			} else if tt, ok := fields["track_type"].(string); ok {
				out["track_type"] = tt
			} else if tt, ok := fields["Type"].(string); ok {
				out["track_type"] = tt
			}
		}
		return out
	}
	if op, ok := m["op"].(string); ok {
		out["op"] = op
	} else if op, ok := m["Op"].(string); ok {
		out["op"] = op
	}
	if k, ok := m["kind"].(string); ok {
		out["kind"] = k
	} else if k, ok := m["Kind"].(string); ok {
		out["kind"] = k
	}
	if tt, ok := m["track_type"].(string); ok {
		out["track_type"] = tt
	}
	return out
}

// payloadAsMap converts an arbitrary payload to map[string]any. Uses
// JSON round-trip for structs so CEL sees field names rather than Go
// reflection. Nil → empty map (CEL needs a non-nil binding).
func payloadAsMap(p any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	if m, ok := p.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// resourceFromPayload extracts {id, kind, fields} from the payload
// when the shape is recognized (PreEntityPayload). For other payloads
// it falls back to {id: "", kind: "", fields: payload-as-map}.
func resourceFromPayload(raw any, m map[string]any) map[string]any {
	if pe, ok := raw.(domain.PreEntityPayload); ok {
		fields := map[string]any{}
		if pe.Entity != nil {
			fields = payloadAsMap(pe.Entity)
		}
		kind := ""
		if k, ok := fields["Kind"].(string); ok {
			kind = k
		} else if k, ok := fields["kind"].(string); ok {
			kind = k
		}
		return map[string]any{
			"id":     pe.EntityID,
			"kind":   kind,
			"fields": fields,
		}
	}
	id, _ := m["EntityID"].(string)
	if id == "" {
		id, _ = m["id"].(string)
	}
	kind, _ := m["Kind"].(string)
	if kind == "" {
		kind, _ = m["kind"].(string)
	}
	return map[string]any{
		"id":     id,
		"kind":   kind,
		"fields": m,
	}
}

// contextAttrsFromCtx pulls the host-supplied attrs map from ctx.
// Returns an empty {note: "", request_attrs: {}} when nothing was
// stuffed — keeps CEL bindings well-typed.
func contextAttrsFromCtx(ctx context.Context) map[string]any {
	out := map[string]any{
		"note":          "",
		"request_attrs": map[string]any{},
	}
	v := ctx.Value(ContextAttrsKey)
	if v == nil {
		return out
	}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	if n, ok := m["note"].(string); ok {
		out["note"] = n
	}
	if ra, ok := m["request_attrs"].(map[string]any); ok {
		out["request_attrs"] = ra
	}
	return out
}
