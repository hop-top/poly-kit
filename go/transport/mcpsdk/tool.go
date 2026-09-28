package mcpsdk

// The tool descriptor (description and inputSchema) and the mapping
// of call arguments back onto flags and positional arguments come
// from cmdsurface (MCPToolDescription, MCPInputSchema,
// MCPSplitArguments), the same functions the hand-rolled surface
// uses: the two live servers cannot publish different schemas for
// one leaf. The wire protocol, by contrast, is never duplicated
// here — it is entirely the SDK's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	taskext "hop.top/mcp-tasks"

	"hop.top/kit/go/transport/cmdsurface"
)

// toolFor renders one bridge leaf as an SDK tool descriptor. Tool
// name is the dotted leaf path (e.g. "widget.add"), identical to the
// hand-rolled surface so clients can switch implementations without
// re-learning tool names. The destructive hint mirrors the bridge's
// safety classification.
func toolFor(leaf *cmdsurface.Leaf) *mcp.Tool {
	destructive := leaf.Class.Destructive
	return &mcp.Tool{
		Name:        toolName(leaf.Path),
		Description: cmdsurface.MCPToolDescription(leaf),
		InputSchema: cmdsurface.MCPInputSchema(leaf),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}
}

// toolHandler binds one leaf to an SDK ToolHandler. Per call it:
//
//  1. Builds the call's provenance (see [WithCallMeta]).
//  2. Applies the auth gate for kit/auth-required leaves (see
//     [WithAuthenticated]; by default an Authorization header).
//  3. Decodes the raw arguments into Invocation.Flags and, for a
//     leaf that declares positional arguments, its "args" array into
//     Invocation.Args (cmdsurface.MCPSplitArguments); flag values
//     are forwarded as-is and re-rendered by the bridge at apply
//     time. A malformed or short "args" is an isError result.
//  4. Admits the call through Bridge.Admit: surface enablement,
//     invocability, the destructive policy ceiling and the permission
//     gate, each refusal audited.
//  5. For a kit/requires-confirmation leaf, only then applies the
//     confirmation gate (the X-Confirm-Token header, or an accepted
//     elicitation with [WithConfirmationElicitation]): a person is
//     never asked about a call a machine gate refuses.
//  6. Runs the admission — Admission.Run, or Admission.Stream for a
//     call carrying a progress token — which audits the outcome.
//
// Error mapping matches the hand-rolled surface's contract: a leaf
// that is unknown or no longer enabled is a protocol error; policy
// blocks, runner failures, and non-zero exit codes are isError
// results so the calling model can read and react to them.
//
// When the tasks binding names the leaf task-eligible and the client
// declares the tasks extension for the request, the call diverts onto
// the SEP-2663 task path (after the auth gate, which applies to every
// path): destructive policy and confirmation are enforced at task
// creation, and execution detaches onto the Runner via Bridge.Invoke.
func (s *Surface) toolHandler(leaf *cmdsurface.Leaf) mcp.ToolHandler {
	b, tb := s.b, s.tasks
	path := append([]string(nil), leaf.Path...)
	cls := leaf.Class
	name := toolName(leaf.Path)
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		inv := cmdsurface.Invocation{Path: path, Meta: s.callMeta(ctx, req)}

		if cls.AuthRequired && !s.authenticated(ctx, req) {
			b.Audit(ctx, inv, cmdsurface.Result{},
				fmt.Errorf("%w: authentication required", cmdsurface.ErrAuthRefused))
			return errorResult("authentication required"), nil
		}
		if tb != nil && tb.eligible[name] && taskext.ClientDeclares(req) {
			return tb.invokeAsTask(ctx, b, leaf, req, headerOf(req))
		}
		var err error
		inv.Flags, inv.Args, err = decodeArguments(leaf, req.Params.Arguments)
		if err != nil {
			if errors.Is(err, errMalformedArguments) {
				return nil, err
			}
			return errorResult(err.Error()), nil
		}

		// Every machine gate answers before anything else happens:
		// resolution, enablement, invocability, the destructive
		// ceiling, the permission gate. A refusal is audited by the
		// bridge.
		adm, err := b.Admit(ctx, inv)
		if err != nil {
			if isUncallable(err) {
				return nil, err
			}
			return errorResult(err.Error()), nil
		}

		// Only then is a person asked: a caller a machine gate refuses
		// never sees a prompt.
		if cls.RequiresConfirmation {
			if res := s.confirmGate(ctx, req, leaf, inv); res != nil {
				return res, nil
			}
		}

		// A progress token opts the call into progressive delivery:
		// the runner streams and each output line becomes an MCP
		// progress notification on the requesting session.
		if token := req.Params.GetProgressToken(); token != nil {
			return streamAdmitted(ctx, adm, req, token)
		}

		res, err := adm.Run(ctx)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return renderResult(res), nil
	}
}

// streamAdmitted runs an admitted call through Admission.Stream,
// forwarding one MCP progress notification per output line to the
// requesting session and returning the terminal Result as the call
// result. The gates have already answered in Bridge.Admit; a progress
// token changes how the call is observed and nothing about whether it
// may run.
func streamAdmitted(ctx context.Context, adm *cmdsurface.Admission, req *mcp.CallToolRequest, token any) (*mcp.CallToolResult, error) {
	events := make(chan cmdsurface.Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- adm.Stream(ctx, events) }()

	var res *cmdsurface.Result
	var progress float64
	for ev := range events {
		switch ev.Kind {
		case "done":
			if r, ok := ev.Data.(*cmdsurface.Result); ok {
				res = r
			}
		case "stdout", "stderr":
			progress++
			line, _ := ev.Data.(string)
			if ev.Kind == "stderr" {
				line = "[stderr] " + line
			}
			// Best-effort: a notification the client misses does not
			// fail the call.
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      progress,
				Message:       line,
			})
		}
	}
	if err := <-errc; err != nil {
		if isUncallable(err) {
			return nil, err
		}
		return errorResult(err.Error()), nil
	}
	if res == nil {
		return errorResult("streaming produced no result"), nil
	}
	return renderResult(*res), nil
}

// isUncallable reports whether err means the tool cannot be called
// at all (as opposed to a call that ran and failed).
func isUncallable(err error) bool {
	return errors.Is(err, cmdsurface.ErrUnknownCommand) ||
		errors.Is(err, cmdsurface.ErrSurfaceNotEnabled)
}

// renderResult maps a bridge Result onto the SDK result type. Layout
// matches the hand-rolled surface: stdout is always the first text
// block, stderr (when present) a second block tagged "[stderr] ",
// and a non-zero exit code sets isError. Structured Data rides in
// the SDK-native structuredContent field rather than a third text
// block.
func renderResult(res cmdsurface.Result) *mcp.CallToolResult {
	content := []mcp.Content{&mcp.TextContent{Text: res.Stdout}}
	if res.Stderr != "" {
		content = append(content, &mcp.TextContent{Text: "[stderr] " + res.Stderr})
	}
	out := &mcp.CallToolResult{
		Content: content,
		IsError: res.ExitCode != 0,
	}
	if res.Data != nil {
		out.StructuredContent = res.Data
	}
	return out
}

// errorResult returns an isError tool result carrying msg as its
// single text block.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// toolName renders a leaf path as a dotted MCP tool name.
func toolName(path []string) string { return strings.Join(path, ".") }

// errMalformedArguments marks arguments that are not a JSON object:
// a protocol error rather than a tool result.
var errMalformedArguments = errors.New("invalid arguments")

// decodeArguments decodes a call's raw arguments object and splits it
// into flags and positional arguments for leaf. Arguments that are
// not a JSON object wrap errMalformedArguments; any other error is
// the caller's to correct from an isError result.
func decodeArguments(leaf *cmdsurface.Leaf, raw json.RawMessage) (map[string]any, []string, error) {
	var arguments map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errMalformedArguments, err)
		}
	}
	return cmdsurface.MCPSplitArguments(leaf, arguments)
}
