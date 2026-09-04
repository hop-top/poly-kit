package mcpsdk

// The flag→JSON-Schema mapping below intentionally duplicates the
// small type-mapping logic in cmdsurface's hand-rolled surface (which
// itself duplicates go/ai/toolspec/adapters/mcp.go). All three call
// sites agree that duplicating ~60 lines beats coupling packages
// that evolve on independent schedules. The wire protocol, by
// contrast, is never duplicated here — it is entirely the SDK's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	taskext "mcpext.example/tasks"

	"hop.top/kit/go/transport/cmdsurface"
)

// toolFor renders one bridge leaf as an SDK tool descriptor. Tool
// name is the dotted leaf path (e.g. "widget.add"), identical to the
// hand-rolled surface so clients can switch implementations without
// re-learning tool names. The destructive hint mirrors the bridge's
// safety classification.
func toolFor(leaf *cmdsurface.Leaf) *mcp.Tool {
	props, required := collectFlags(leaf.Cmd)
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	destructive := leaf.Class.Destructive
	return &mcp.Tool{
		Name:        toolName(leaf.Path),
		Description: leaf.Cmd.Short,
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}
}

// toolHandler binds one leaf to an SDK ToolHandler. Per call it:
//
//  1. Applies the auth + confirmation gates from the leaf's safety
//     class against the transport's HTTP headers (absent headers —
//     e.g. on stdio — fail closed).
//  2. Decodes the raw arguments into Invocation.Flags; values are
//     forwarded as-is and re-rendered by the bridge at apply time.
//  3. Dispatches through Bridge.Invoke, which re-checks surface
//     enablement and the destructive policy ceiling.
//
// Error mapping matches the hand-rolled surface's contract: a leaf
// that is unknown or no longer enabled is a protocol error; policy
// blocks, runner failures, and non-zero exit codes are isError
// results so the calling model can read and react to them.
//
// When tb names the leaf task-eligible and the client declares the
// tasks extension for the request, the call diverts onto the SEP-2663
// task path (after the auth gate, which applies to every path):
// destructive policy and confirmation are enforced at task creation,
// and execution detaches onto the Runner via Bridge.Invoke.
func toolHandler(b *cmdsurface.Bridge, leaf *cmdsurface.Leaf, tb *taskBinding) mcp.ToolHandler {
	path := append([]string(nil), leaf.Path...)
	cls := leaf.Class
	name := toolName(leaf.Path)
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var hdr http.Header
		if req.Extra != nil {
			hdr = req.Extra.Header
		}
		if cls.AuthRequired && hdr.Get("Authorization") == "" {
			return errorResult("authentication required"), nil
		}
		if tb != nil && tb.eligible[name] && taskext.ClientDeclares(req) {
			return tb.invokeAsTask(ctx, b, leaf, req, hdr)
		}
		if cls.RequiresConfirmation && hdr.Get("X-Confirm-Token") == "" {
			return errorResult("confirmation required"), nil
		}

		var flags map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &flags); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}

		inv := cmdsurface.Invocation{
			Path:  path,
			Flags: flags,
			Meta: cmdsurface.Meta{
				Surface:     cmdsurface.SurfaceMCP,
				RequestedAt: time.Now(),
			},
		}

		// A progress token opts the call into progressive delivery:
		// the runner streams and each output line becomes an MCP
		// progress notification on the requesting session.
		if token := req.Params.GetProgressToken(); token != nil {
			return streamInvoke(ctx, b, leaf, inv, req, token)
		}

		res, err := b.Invoke(ctx, inv)
		if err != nil {
			if isUncallable(err) {
				return nil, err
			}
			return errorResult(err.Error()), nil
		}
		return renderResult(res), nil
	}
}

// streamInvoke runs inv through Runner.Stream, forwarding one MCP
// progress notification per output line to the requesting session
// and returning the terminal Result as the call result. The policy
// gates Bridge.Invoke would apply are applied here first, the same
// way the bridge's other streaming surfaces do before reaching the
// Runner directly.
func streamInvoke(ctx context.Context, b *cmdsurface.Bridge, leaf *cmdsurface.Leaf, inv cmdsurface.Invocation, req *mcp.CallToolRequest, token any) (*mcp.CallToolResult, error) {
	if !leaf.Enabled[cmdsurface.SurfaceMCP] {
		return nil, fmt.Errorf("%w: %s on %s",
			cmdsurface.ErrSurfaceNotEnabled, leaf.PathKey(), cmdsurface.SurfaceMCP)
	}
	if !b.Policy().Allowed(leaf.Class, cmdsurface.SurfaceMCP) {
		return errorResult(fmt.Sprintf("%v: %s on %s",
			cmdsurface.ErrDestructiveBlocked, leaf.PathKey(), cmdsurface.SurfaceMCP)), nil
	}

	events := make(chan cmdsurface.Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- b.Runner().Stream(ctx, inv, events) }()

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

// jsonType maps a pflag type string to the corresponding JSON Schema
// primitive.
func jsonType(pflagType string) string {
	switch pflagType {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"count":
		return "integer"
	case "float32", "float64":
		return "number"
	case "stringArray", "stringSlice", "intSlice", "boolSlice":
		return "array"
	default:
		return "string"
	}
}

// flagProperty maps one pflag.Flag to a JSON Schema property object.
func flagProperty(f *pflag.Flag) map[string]any {
	t := jsonType(f.Value.Type())
	prop := map[string]any{
		"type":        t,
		"description": f.Usage,
	}
	if t == "array" {
		prop["items"] = map[string]string{"type": "string"}
	}
	return prop
}

// isFlagRequired reports whether cobra's MarkFlagRequired annotation
// is set on f.
func isFlagRequired(f *pflag.Flag) bool {
	_, ok := f.Annotations[cobra.BashCompOneRequiredFlag]
	return ok
}

// collectFlags walks both local and inherited flags of cmd and
// returns the schema properties + required-name list, filtering out
// hidden / deprecated flags. Local flags win over inherited ones of
// the same name.
func collectFlags(cmd *cobra.Command) (map[string]any, []string) {
	props := make(map[string]any)
	var required []string
	seen := make(map[string]bool)

	visit := func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		props[f.Name] = flagProperty(f)
		if isFlagRequired(f) {
			required = append(required, f.Name)
		}
	}

	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)
	return props, required
}
