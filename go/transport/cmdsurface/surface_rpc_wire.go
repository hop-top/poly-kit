package cmdsurface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// Conversions between the Go types the Bridge runs on and the
// cmdsurface.v1 messages the RPC surface puts on the wire. The schema
// source of truth is contracts/proto/cmdsurface/v1/commands.proto.

// invocationFromProto decodes a wire Invocation. Flag numbers arrive as
// float64, the type encoding/json produced for the pre-proto wire.
func invocationFromProto(m *cmdsurfacev1.Invocation) Invocation {
	inv := Invocation{
		Path: append([]string(nil), m.GetPath()...),
		Args: append([]string(nil), m.GetArgs()...),
	}
	if f := m.GetFlags(); len(f.GetFields()) > 0 {
		inv.Flags = f.AsMap()
	}
	md := m.GetMeta()
	inv.Meta = Meta{
		Caller:         md.GetCaller(),
		Tenant:         md.GetTenant(),
		Surface:        Surface(md.GetSurface()),
		RequestID:      md.GetRequestId(),
		TraceID:        md.GetTraceId(),
		IdempotencyKey: md.GetIdempotencyKey(),
	}
	if ts := md.GetRequestedAt(); ts != nil {
		inv.Meta.RequestedAt = ts.AsTime()
	}
	if extra := md.GetExtra(); len(extra) > 0 {
		inv.Meta.Extra = make(map[string]string, len(extra))
		for k, v := range extra {
			inv.Meta.Extra[k] = v
		}
	}
	return inv
}

// resultToProto encodes res. exit_code is always set so the JSON form
// carries it even when zero.
func resultToProto(res Result) (*cmdsurfacev1.Result, error) {
	out := &cmdsurfacev1.Result{
		ExitCode: proto.Int32(clampInt32(res.ExitCode)),
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}
	if res.Data != nil {
		v, raw, err := wireValue(res.Data)
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: encode result data: %w", err)
		}
		out.Data, out.DataJson = v, raw
	}
	return out, nil
}

// eventToProto encodes ev. A Result payload (the terminal "done"
// event) fills both result and data: data keeps the JSON shape clients
// read before the typed field existed.
func eventToProto(ev Event) (*cmdsurfacev1.Event, error) {
	out := &cmdsurfacev1.Event{Kind: ev.Kind, At: timestamppb.New(ev.At)}
	if ev.Data == nil {
		return out, nil
	}
	var res *Result
	switch d := ev.Data.(type) {
	case *Result:
		res = d
	case Result:
		res = &d
	}
	if res != nil {
		pr, err := resultToProto(*res)
		if err != nil {
			return nil, err
		}
		out.Result = pr
	}
	v, _, err := wireValue(ev.Data)
	if err != nil {
		return nil, fmt.Errorf("cmdsurface: encode %s event data: %w", ev.Kind, err)
	}
	out.Data = v
	return out, nil
}

// wireValue encodes v as the JSON the command would have written and
// converts that JSON to a google.protobuf.Value. It returns the JSON
// text too, compact and without HTML escaping. json.Number values keep
// their digits in the text; in the Value they stay numbers when the
// nearest double reads back as the same value, else become strings.
func wireValue(v any) (*structpb.Value, string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, "", err
	}
	raw := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, "", err
	}
	val, err := valueOf(tree)
	if err != nil {
		return nil, "", err
	}
	return val, string(raw), nil
}

// valueOf converts a JSON tree decoded with UseNumber.
func valueOf(x any) (*structpb.Value, error) {
	switch t := x.(type) {
	case nil:
		return structpb.NewNullValue(), nil
	case bool:
		return structpb.NewBoolValue(t), nil
	case string:
		return structpb.NewStringValue(t), nil
	case json.Number:
		return numberValue(t), nil
	case []any:
		vals := make([]*structpb.Value, len(t))
		for i, e := range t {
			v, err := valueOf(e)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		return structpb.NewListValue(&structpb.ListValue{Values: vals}), nil
	case map[string]any:
		fields := make(map[string]*structpb.Value, len(t))
		for k, e := range t {
			v, err := valueOf(e)
			if err != nil {
				return nil, err
			}
			fields[k] = v
		}
		return structpb.NewStructValue(&structpb.Struct{Fields: fields}), nil
	default:
		return nil, fmt.Errorf("unexpected JSON value %T", x)
	}
}

// numberValue keeps n a number when its nearest double, printed in
// shortest form, has the same value as n's text; otherwise it carries
// n's digits as a string so no reader sees a rounded value.
func numberValue(n json.Number) *structpb.Value {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil { // out of range: Inf or underflow
		return structpb.NewStringValue(string(n))
	}
	want, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return structpb.NewStringValue(string(n))
	}
	got, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
	if got == nil || got.Cmp(want) != 0 {
		return structpb.NewStringValue(string(n))
	}
	return structpb.NewNumberValue(f)
}

// clampInt32 narrows an exit status to the wire's int32.
func clampInt32(n int) int32 {
	switch {
	case n > math.MaxInt32:
		return math.MaxInt32
	case n < math.MinInt32:
		return math.MinInt32
	default:
		return int32(n)
	}
}
