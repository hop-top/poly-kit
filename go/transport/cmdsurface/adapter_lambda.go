package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// LambdaEventType selects how an inbound AWS Lambda event payload is
// mapped to a bridge Invocation. Adopters pick the type that matches
// the trigger they wired in their Lambda configuration (API Gateway,
// EventBridge rule, SQS queue, direct invoke).
type LambdaEventType string

const (
	// EventAPIGatewayV2 expects [events.APIGatewayV2HTTPRequest]
	// payloads. Used by API Gateway HTTP APIs and Function URLs.
	EventAPIGatewayV2 LambdaEventType = "apigw_v2"
	// EventAPIGatewayV1 expects [events.APIGatewayProxyRequest]
	// payloads. Used by API Gateway REST APIs.
	EventAPIGatewayV1 LambdaEventType = "apigw_v1"
	// EventEventBridge expects [events.CloudWatchEvent] payloads
	// (EventBridge rule targets, scheduled events).
	EventEventBridge LambdaEventType = "eventbridge"
	// EventSQS expects [events.SQSEvent] payloads. The handler
	// invokes the bridge once per record and reports per-record
	// failures via [events.SQSEventResponse] so Lambda re-delivers
	// only the failed messages.
	EventSQS LambdaEventType = "sqs"
	// EventDirect treats the raw event JSON as an [Invocation]
	// literal. Useful for service-to-service Lambda invokes that
	// already speak the bridge's wire format.
	EventDirect LambdaEventType = "direct"
)

// LambdaConfig declares how [LambdaHandler] builds its closure: which
// event type to expect, how to map that event onto a bridge
// [Invocation], and how to observe each result.
type LambdaConfig struct {
	// Event selects the inbound event family. Required.
	Event LambdaEventType
	// Mapping declares the leaf and template-driven flag / args
	// extraction. Ignored when Event == EventDirect (the event JSON
	// itself is the Invocation).
	Mapping LambdaMapping
	// ResultLog is invoked once per bridge call with the originating
	// config, the bridge Result, and any error returned by the
	// bridge. For SQS events the callback fires once per record.
	// Adopters wire this to CloudWatch logging in their handler.
	ResultLog func(LambdaConfig, Result, error)
}

// LambdaMapping configures the event → Invocation transform. The
// template engine and root-key allow-list are shared with the Webhook
// surface (see [parseWebhookTemplate]); the per-event-type root is
// described in the [LambdaHandler] doc.
type LambdaMapping struct {
	// Path is the leaf command path (e.g. ["widget","add"]). Required.
	Path []string
	// FlagMap maps flag name → text/template source. Each template
	// renders against an event-specific root and yields a single
	// flag value. An empty rendered value omits the flag.
	FlagMap map[string]string
	// ArgsTemplate is an optional template whose rendered value is
	// split on whitespace and used as positional Invocation args.
	ArgsTemplate string
}

// LambdaHandler builds an AWS Lambda handler closure from b. The
// returned function has signature
//
//	func(ctx context.Context, event json.RawMessage) (json.RawMessage, error)
//
// which matches what [lambda.NewHandlerWithOptions] accepts when
// paired with a custom unmarshaler (the standard
// [lambda.Start](handler) entrypoint accepts the same shape directly
// when the handler signature uses json.RawMessage).
//
// Typical adopter wiring at process scope:
//
//	func main() {
//	    b := buildBridge() // construct once
//	    h, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
//	        Event:   cmdsurface.EventAPIGatewayV2,
//	        Mapping: cmdsurface.LambdaMapping{
//	            Path:    []string{"widget", "add"},
//	            FlagMap: map[string]string{"name": "{{ .body.title }}"},
//	        },
//	    })
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    lambda.Start(h)
//	}
//
// The bridge is captured in the closure once and reused across
// invocations — this is the "init-once Runner" pattern Lambda warm
// containers need.
//
// LambdaHandler validates the mapping eagerly: an unknown leaf, a
// leaf without SurfaceFaaS enabled, a destructive leaf without
// policy opt-in, or a confirmation-required leaf each produce a
// non-nil error. The caller decides how to surface that (log.Fatal
// is the common choice). Template parse errors are reported the
// same way.
//
// Per-event behavior:
//
//   - EventAPIGatewayV2 / EventAPIGatewayV1: the template root is
//     {body, headers, query, path, detail} where body is the
//     JSON-decoded request body (nil otherwise), headers/query/path
//     are the event's flattened single-value maps, and detail is
//     nil. Responses are an [events.APIGatewayV2HTTPResponse] (or
//     v1 equivalent) with StatusCode derived from the bridge error
//     class (see the package error table) or 200/500 from the
//     command's ExitCode.
//   - EventEventBridge: the template root is {body, headers, query,
//     path, detail} where detail is the JSON-decoded event.Detail
//     and the other keys are nil. The response is the bridge Result
//     marshaled as JSON; errors propagate as the Go error.
//   - EventSQS: one bridge call per record. Per-record templates
//     use {body, headers, query, path, detail} where body is the
//     JSON-decoded message body, headers is the flattened
//     MessageAttributes (stringValue per attribute), and the other
//     keys are nil. The response is an [events.SQSEventResponse]
//     whose BatchItemFailures lists messageIds for records whose
//     invocation errored or returned ExitCode != 0; successful
//     records are absent.
//   - EventDirect: the raw event JSON is unmarshalled into an
//     Invocation; Meta.Surface and Meta.Caller are forced to
//     SurfaceFaaS and "lambda" respectively. The response is the
//     bridge Result marshaled as JSON.
func LambdaHandler(b *Bridge, cfg LambdaConfig) (func(ctx context.Context, event json.RawMessage) (json.RawMessage, error), error) {
	if b == nil {
		return nil, errors.New("cmdsurface: nil Bridge")
	}
	if cfg.Event == "" {
		return nil, errors.New("cmdsurface: LambdaConfig.Event is required")
	}

	// EventDirect skips mapping validation — the event JSON IS the
	// invocation, so the path is unknown until runtime. We still
	// require a non-nil bridge above.
	if cfg.Event == EventDirect {
		return newLambdaDirectHandler(b, cfg), nil
	}

	if len(cfg.Mapping.Path) == 0 {
		return nil, errors.New("cmdsurface: LambdaConfig.Mapping.Path is required")
	}

	leaf, err := b.resolveLeaf(cfg.Mapping.Path)
	if err != nil {
		return nil, err
	}
	if !leaf.Enabled[SurfaceFaaS] {
		return nil, fmt.Errorf("%w: %s on %s",
			ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceFaaS)
	}
	if leaf.Class.Destructive && !b.cfg.policy.Allowed(leaf.Class, SurfaceFaaS) {
		return nil, fmt.Errorf("%w: %s on %s",
			ErrDestructiveBlocked, leaf.PathKey(), SurfaceFaaS)
	}
	if leaf.Class.RequiresConfirmation {
		return nil, fmt.Errorf("cmdsurface: leaf %q requires confirmation; Lambda has no confirm-token channel",
			leaf.PathKey())
	}
	// Class.AuthRequired: the IAM permission to invoke the function
	// is the auth; the bridge has no header to check here.

	flagTmpls := make(map[string]*template.Template, len(cfg.Mapping.FlagMap))
	for k, src := range cfg.Mapping.FlagMap {
		t, err := parseLambdaTemplate("lambda:flag:"+k, src)
		if err != nil {
			return nil, err
		}
		flagTmpls[k] = t
	}
	var argsTmpl *template.Template
	if cfg.Mapping.ArgsTemplate != "" {
		t, err := parseLambdaTemplate("lambda:args", cfg.Mapping.ArgsTemplate)
		if err != nil {
			return nil, err
		}
		argsTmpl = t
	}

	switch cfg.Event {
	case EventAPIGatewayV2:
		return newLambdaAPIGWv2Handler(b, leaf, cfg, flagTmpls, argsTmpl), nil
	case EventAPIGatewayV1:
		return newLambdaAPIGWv1Handler(b, leaf, cfg, flagTmpls, argsTmpl), nil
	case EventEventBridge:
		return newLambdaEventBridgeHandler(b, leaf, cfg, flagTmpls, argsTmpl), nil
	case EventSQS:
		return newLambdaSQSHandler(b, leaf, cfg, flagTmpls, argsTmpl), nil
	default:
		return nil, fmt.Errorf("cmdsurface: unknown LambdaEventType %q", cfg.Event)
	}
}

// newLambdaAPIGWv2Handler returns the handler closure for API
// Gateway V2 / Function URL events.
func newLambdaAPIGWv2Handler(
	b *Bridge,
	leaf *Leaf,
	cfg LambdaConfig,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, event json.RawMessage) (json.RawMessage, error) {
		var req events.APIGatewayV2HTTPRequest
		if err := json.Unmarshal(event, &req); err != nil {
			return marshalAPIGWv2Response(400, lambdaErrorBody("bad_request", err.Error()))
		}
		root := buildAPIGWv2Root(req)
		args, flags, terr := lambdaRenderInvocation(flagTmpls, argsTmpl, root)
		if terr != nil {
			return marshalAPIGWv2Response(400, lambdaErrorBody("template_error", terr.Error()))
		}
		inv := Invocation{
			Path:  append([]string(nil), leaf.Path...),
			Args:  args,
			Flags: flags,
			Meta: Meta{
				Surface:     SurfaceFaaS,
				Caller:      "lambda",
				TraceID:     req.Headers["x-request-id"],
				RequestedAt: time.Now(),
			},
		}
		res, err := b.Invoke(ctx, inv)
		if cfg.ResultLog != nil {
			cfg.ResultLog(cfg, res, err)
		}
		if err != nil {
			status, code := lambdaHTTPErrorCode(err)
			return marshalAPIGWv2Response(status, lambdaErrorBody(code, err.Error()))
		}
		status := 200
		if res.ExitCode != 0 {
			status = 500
		}
		body, mErr := json.Marshal(res)
		if mErr != nil {
			return nil, mErr
		}
		return marshalAPIGWv2Response(status, body)
	}
}

// newLambdaAPIGWv1Handler returns the handler closure for API
// Gateway V1 REST events.
func newLambdaAPIGWv1Handler(
	b *Bridge,
	leaf *Leaf,
	cfg LambdaConfig,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, event json.RawMessage) (json.RawMessage, error) {
		var req events.APIGatewayProxyRequest
		if err := json.Unmarshal(event, &req); err != nil {
			return marshalAPIGWv1Response(400, lambdaErrorBody("bad_request", err.Error()))
		}
		root := buildAPIGWv1Root(req)
		args, flags, terr := lambdaRenderInvocation(flagTmpls, argsTmpl, root)
		if terr != nil {
			return marshalAPIGWv1Response(400, lambdaErrorBody("template_error", terr.Error()))
		}
		inv := Invocation{
			Path:  append([]string(nil), leaf.Path...),
			Args:  args,
			Flags: flags,
			Meta: Meta{
				Surface:     SurfaceFaaS,
				Caller:      "lambda",
				TraceID:     req.Headers["X-Request-Id"],
				RequestedAt: time.Now(),
			},
		}
		res, err := b.Invoke(ctx, inv)
		if cfg.ResultLog != nil {
			cfg.ResultLog(cfg, res, err)
		}
		if err != nil {
			status, code := lambdaHTTPErrorCode(err)
			return marshalAPIGWv1Response(status, lambdaErrorBody(code, err.Error()))
		}
		status := 200
		if res.ExitCode != 0 {
			status = 500
		}
		body, mErr := json.Marshal(res)
		if mErr != nil {
			return nil, mErr
		}
		return marshalAPIGWv1Response(status, body)
	}
}

// newLambdaEventBridgeHandler returns the handler closure for
// EventBridge / CloudWatchEvent payloads.
func newLambdaEventBridgeHandler(
	b *Bridge,
	leaf *Leaf,
	cfg LambdaConfig,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, event json.RawMessage) (json.RawMessage, error) {
		var ev events.CloudWatchEvent
		if err := json.Unmarshal(event, &ev); err != nil {
			return nil, fmt.Errorf("cmdsurface: decode eventbridge event: %w", err)
		}
		root := buildEventBridgeRoot(ev)
		args, flags, terr := lambdaRenderInvocation(flagTmpls, argsTmpl, root)
		if terr != nil {
			return nil, fmt.Errorf("cmdsurface: template: %w", terr)
		}
		inv := Invocation{
			Path:  append([]string(nil), leaf.Path...),
			Args:  args,
			Flags: flags,
			Meta: Meta{
				Surface:     SurfaceFaaS,
				Caller:      "lambda",
				TraceID:     ev.ID,
				RequestedAt: time.Now(),
			},
		}
		res, err := b.Invoke(ctx, inv)
		if cfg.ResultLog != nil {
			cfg.ResultLog(cfg, res, err)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// newLambdaSQSHandler returns the handler closure for SQS events.
// Each record is invoked independently; per-record failures are
// reported via SQSEventResponse.BatchItemFailures so Lambda only
// re-delivers the failed messages.
func newLambdaSQSHandler(
	b *Bridge,
	leaf *Leaf,
	cfg LambdaConfig,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, event json.RawMessage) (json.RawMessage, error) {
		var ev events.SQSEvent
		if err := json.Unmarshal(event, &ev); err != nil {
			return nil, fmt.Errorf("cmdsurface: decode sqs event: %w", err)
		}
		var failures []events.SQSBatchItemFailure
		for _, rec := range ev.Records {
			recCopy := rec
			failed := invokeSQSRecord(ctx, b, leaf, cfg, flagTmpls, argsTmpl, recCopy)
			if failed {
				failures = append(failures, events.SQSBatchItemFailure{
					ItemIdentifier: recCopy.MessageId,
				})
			}
		}
		resp := events.SQSEventResponse{BatchItemFailures: failures}
		return json.Marshal(resp)
	}
}

// invokeSQSRecord runs one SQS record through the bridge and reports
// whether the record should be retried (failed=true).
func invokeSQSRecord(
	ctx context.Context,
	b *Bridge,
	leaf *Leaf,
	cfg LambdaConfig,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
	rec events.SQSMessage,
) bool {
	root := buildSQSRoot(rec)
	args, flags, terr := lambdaRenderInvocation(flagTmpls, argsTmpl, root)
	if terr != nil {
		if cfg.ResultLog != nil {
			cfg.ResultLog(cfg, Result{}, fmt.Errorf("template: %w", terr))
		}
		return true
	}
	inv := Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Args:  args,
		Flags: flags,
		Meta: Meta{
			Surface:     SurfaceFaaS,
			Caller:      "lambda",
			TraceID:     rec.MessageId,
			RequestedAt: time.Now(),
		},
	}
	res, err := b.Invoke(ctx, inv)
	if cfg.ResultLog != nil {
		cfg.ResultLog(cfg, res, err)
	}
	if err != nil {
		return true
	}
	return res.ExitCode != 0
}

// newLambdaDirectHandler returns the handler closure for raw
// Invocation payloads (no event-shape unmarshal, no templates).
func newLambdaDirectHandler(b *Bridge, cfg LambdaConfig) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, event json.RawMessage) (json.RawMessage, error) {
		var inv Invocation
		if err := json.Unmarshal(event, &inv); err != nil {
			return nil, fmt.Errorf("cmdsurface: decode direct invocation: %w", err)
		}
		inv.Meta.Surface = SurfaceFaaS
		inv.Meta.Caller = "lambda"
		inv.Meta.RequestedAt = time.Now()
		res, err := b.Invoke(ctx, inv)
		if cfg.ResultLog != nil {
			cfg.ResultLog(cfg, res, err)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// lambdaRenderInvocation executes the flag and args templates
// against root and returns the resulting (args, flags). An empty
// rendered flag is omitted; an empty args template yields nil.
func lambdaRenderInvocation(
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
	root map[string]any,
) ([]string, map[string]any, error) {
	flags := make(map[string]any, len(flagTmpls))
	for k, t := range flagTmpls {
		val, err := execWebhookTemplate(t, root)
		if err != nil {
			return nil, nil, err
		}
		if val == "" {
			continue
		}
		flags[k] = val
	}
	var args []string
	if argsTmpl != nil {
		val, err := execWebhookTemplate(argsTmpl, root)
		if err != nil {
			return nil, nil, err
		}
		if val != "" {
			args = strings.Fields(val)
		}
	}
	return args, flags, nil
}

// buildAPIGWv2Root assembles the template root for API Gateway V2
// requests: {body, headers, query, path, detail}. body is the JSON-
// decoded request body when Content-Type indicates JSON; otherwise
// an empty map. detail is an empty map (EventBridge-only key).
func buildAPIGWv2Root(req events.APIGatewayV2HTTPRequest) map[string]any {
	var bodyJSON map[string]any
	if isJSONContent(headerValueCI(req.Headers, "content-type")) && req.Body != "" {
		_ = json.Unmarshal([]byte(req.Body), &bodyJSON)
	}
	if bodyJSON == nil {
		bodyJSON = map[string]any{}
	}
	return map[string]any{
		"body":    bodyJSON,
		"headers": copyStringMap(req.Headers),
		"query":   copyStringMap(req.QueryStringParameters),
		"path":    copyStringMap(req.PathParameters),
		"detail":  map[string]any{},
	}
}

// buildAPIGWv1Root assembles the template root for API Gateway V1
// REST requests. Same shape as V2.
func buildAPIGWv1Root(req events.APIGatewayProxyRequest) map[string]any {
	var bodyJSON map[string]any
	if isJSONContent(headerValueCI(req.Headers, "content-type")) && req.Body != "" {
		_ = json.Unmarshal([]byte(req.Body), &bodyJSON)
	}
	if bodyJSON == nil {
		bodyJSON = map[string]any{}
	}
	return map[string]any{
		"body":    bodyJSON,
		"headers": copyStringMap(req.Headers),
		"query":   copyStringMap(req.QueryStringParameters),
		"path":    copyStringMap(req.PathParameters),
		"detail":  map[string]any{},
	}
}

// buildEventBridgeRoot assembles the template root for EventBridge
// events. detail carries the JSON-decoded event.Detail (empty map
// on decode failure or absence); the HTTP-shaped keys are empty so
// templates that reach for them render the stdlib "<no value>"
// sentinel rather than failing with nil-dereferences.
func buildEventBridgeRoot(ev events.CloudWatchEvent) map[string]any {
	var detail map[string]any
	if len(ev.Detail) > 0 {
		_ = json.Unmarshal(ev.Detail, &detail)
	}
	if detail == nil {
		detail = map[string]any{}
	}
	return map[string]any{
		"body":    map[string]any{},
		"headers": map[string]string{},
		"query":   map[string]string{},
		"path":    map[string]string{},
		"detail":  detail,
	}
}

// buildSQSRoot assembles the template root for one SQS record.
// body is the JSON-decoded message body (empty map on failure);
// headers is the flattened MessageAttributes (StringValue per key);
// the other shape keys are empty.
func buildSQSRoot(rec events.SQSMessage) map[string]any {
	var bodyJSON map[string]any
	if rec.Body != "" {
		_ = json.Unmarshal([]byte(rec.Body), &bodyJSON)
	}
	if bodyJSON == nil {
		bodyJSON = map[string]any{}
	}
	headers := make(map[string]string, len(rec.MessageAttributes))
	for k, attr := range rec.MessageAttributes {
		if attr.StringValue != nil {
			headers[k] = *attr.StringValue
		}
	}
	return map[string]any{
		"body":    bodyJSON,
		"headers": headers,
		"query":   map[string]string{},
		"path":    map[string]string{},
		"detail":  map[string]any{},
	}
}

// headerValueCI returns the first matching header value, comparing
// keys case-insensitively. API Gateway V2 lower-cases header keys
// while V1 preserves case — handling both lets the same root builder
// serve both surfaces.
func headerValueCI(h map[string]string, key string) string {
	if h == nil {
		return ""
	}
	if v, ok := h[key]; ok {
		return v
	}
	lowKey := strings.ToLower(key)
	for k, v := range h {
		if strings.ToLower(k) == lowKey {
			return v
		}
	}
	return ""
}

// copyStringMap returns a defensive copy of m so the template root
// does not alias caller state.
func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// lambdaHTTPErrorCode maps a bridge sentinel error to the (status,
// code) tuple used in API Gateway responses. Unrecognized errors
// fall through to 500 / "internal_error".
func lambdaHTTPErrorCode(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		return 500, "unknown_command"
	case errors.Is(err, ErrSurfaceNotEnabled):
		return 403, "not_enabled"
	case errors.Is(err, ErrDestructiveBlocked):
		return 403, "destructive_blocked"
	default:
		return 500, "internal_error"
	}
}

// lambdaErrorBody returns a JSON-encoded {code,message} envelope
// suitable as the body of an API Gateway response.
func lambdaErrorBody(code, message string) []byte {
	b, err := json.Marshal(map[string]string{"code": code, "message": message})
	if err != nil {
		// Marshal of a string map cannot fail in practice; fall back
		// to a literal so the handler still produces valid JSON.
		return []byte(`{"code":"internal_error","message":"marshal failed"}`)
	}
	return b
}

// marshalAPIGWv2Response wraps body as an APIGatewayV2HTTPResponse
// with Content-Type: application/json and returns the JSON-encoded
// envelope ready for the Lambda runtime to return.
func marshalAPIGWv2Response(status int, body []byte) (json.RawMessage, error) {
	resp := events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
	return json.Marshal(resp)
}

// marshalAPIGWv1Response wraps body as an APIGatewayProxyResponse
// with Content-Type: application/json.
func marshalAPIGWv1Response(status int, body []byte) (json.RawMessage, error) {
	resp := events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
	return json.Marshal(resp)
}

// lambdaAllowedRoots is the closed set of top-level fields a Lambda
// adapter FlagMap / ArgsTemplate may reference. It widens the
// Webhook allow-list with .detail to support EventBridge events
// while still rejecting templates that walk into adversary-named
// roots.
var lambdaAllowedRoots = map[string]struct{}{
	"body":    {},
	"headers": {},
	"query":   {},
	"path":    {},
	"detail":  {},
}

// parseLambdaTemplate parses src and rejects any top-level field
// reference outside lambdaAllowedRoots. The returned template is
// safe to execute against the Lambda adapter's per-event root.
func parseLambdaTemplate(name, src string) (*template.Template, error) {
	t, err := template.New(name).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", name, err)
	}
	if err := checkLambdaTemplateRoots(t); err != nil {
		return nil, fmt.Errorf("template %q: %w", name, err)
	}
	return t, nil
}

// checkLambdaTemplateRoots walks every action node in t and rejects
// templates referencing a top-level field outside the allow-list.
func checkLambdaTemplateRoots(t *template.Template) error {
	if t == nil || t.Root == nil {
		return nil
	}
	return walkLambdaList(t.Root)
}

func walkLambdaList(list *parse.ListNode) error {
	if list == nil {
		return nil
	}
	for _, n := range list.Nodes {
		if err := walkLambdaNode(n); err != nil {
			return err
		}
	}
	return nil
}

func walkLambdaNode(n parse.Node) error {
	switch v := n.(type) {
	case *parse.ActionNode:
		return walkLambdaPipe(v.Pipe)
	case *parse.IfNode:
		if err := walkLambdaPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkLambdaList(v.List); err != nil {
			return err
		}
		return walkLambdaList(v.ElseList)
	case *parse.RangeNode:
		if err := walkLambdaPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkLambdaList(v.List); err != nil {
			return err
		}
		return walkLambdaList(v.ElseList)
	case *parse.WithNode:
		if err := walkLambdaPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkLambdaList(v.List); err != nil {
			return err
		}
		return walkLambdaList(v.ElseList)
	case *parse.TemplateNode:
		return walkLambdaPipe(v.Pipe)
	}
	return nil
}

func walkLambdaPipe(p *parse.PipeNode) error {
	if p == nil {
		return nil
	}
	for _, c := range p.Cmds {
		for _, arg := range c.Args {
			if err := walkLambdaArg(arg); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkLambdaArg(arg parse.Node) error {
	switch v := arg.(type) {
	case *parse.FieldNode:
		if len(v.Ident) == 0 {
			return nil
		}
		if _, ok := lambdaAllowedRoots[v.Ident[0]]; !ok {
			return fmt.Errorf("disallowed template root .%s (allowed: body, headers, query, path, detail)", v.Ident[0])
		}
	case *parse.PipeNode:
		return walkLambdaPipe(v)
	}
	return nil
}
