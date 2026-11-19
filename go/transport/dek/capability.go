package dek

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"hop.top/kit/go/transport/api"
)

// Gate is a dek approval class id: "free", "apply", "review", or an
// app's "<namespace>.<local>".
type Gate = string

// Gates dek defines.
const (
	GateFree   Gate = "free"
	GateApply  Gate = "apply"
	GateReview Gate = "review"
)

// OutputKind is the shape of a capability's result.
type OutputKind string

// Output kinds in the contract.
const (
	OutputList   OutputKind = "list"
	OutputRecord OutputKind = "record"
	OutputPlan   OutputKind = "plan"
	OutputStream OutputKind = "stream"
)

// Targets a capability works on. A kit command runs wherever the
// palette does, so every capability lists all of them.
var allTargets = []string{"desktop", "tablet", "phone", "web", "terminal"}

// Capability is one manifest entry: a noun-verb action the tool
// offers. Field names and shapes mirror the dek contract's Capability.
type Capability struct {
	ID          string         `json:"id"`
	Noun        string         `json:"noun"`
	Verb        string         `json:"verb"`
	ArgsSchema  map[string]any `json:"args_schema"`
	Scopes      []string       `json:"scopes"`
	DryRun      bool           `json:"dry_run"`
	Destructive bool           `json:"destructive"`
	Gate        Gate           `json:"gate"`
	Output      OutputKind     `json:"output"`
	Targets     []string       `json:"targets"`
	Read        bool           `json:"read"`
	Granted     bool           `json:"granted"`

	// desc is the command the capability fronts.
	desc api.CommandDescriptor
	// positional names the declared positional arguments, in order.
	positional []string
	required   map[string]bool
	props      map[string]string // arg name -> json type
}

// Options tune [Manifest].
type Options struct {
	// ToolName is the noun of a capability whose command is a single
	// path segment (the tool's own verb). Empty uses the segment.
	ToolName string
	// Gate picks the approval class of a command. Returning "" keeps
	// the default: free for read and write, review for destructive.
	// The class is a promise that the tool enforces it; kit serves
	// only "free" calls directly (see [Core]).
	Gate func(api.CommandDescriptor) Gate
}

// Manifest builds the capability manifest from reflected commands:
// every invocable command becomes one capability, in id order.
// Non-invocable commands are left out: a manifest lists what can be
// called, and discovery on the REST projection keeps the reasons.
//
// Mapping:
//   - id, noun, verb: the command path, joined with dots; the last
//     segment is the verb, the rest the noun. A single-segment command
//     takes [Options.ToolName] as noun.
//   - args_schema: a JSON Schema object whose properties are the
//     command's flags (typed from the flag type) and positionals
//     (strings), without the kit-wide dry-run flag: a call carries
//     that in its dry_run field.
//   - read, destructive: the side-effect tier.
//   - dry_run (a plan is needed first): destructive commands. Write
//     commands accept a dry-run call but may also run directly.
//   - output: plan for a dry_run capability; else list when the
//     declared output schema is an array, record otherwise.
//
// A flag and a positional sharing a name are refused: both would be
// one key in args.
func Manifest(descs []api.CommandDescriptor, opts Options) ([]Capability, error) {
	var out []Capability
	seen := map[string]bool{}
	for _, d := range descs {
		if !d.Invocable || len(d.Path) == 0 {
			continue
		}
		c, err := capabilityFor(d, opts)
		if err != nil {
			return nil, err
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("dek: two commands map to capability %q", c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func capabilityFor(d api.CommandDescriptor, opts Options) (Capability, error) {
	path := d.Path
	c := Capability{
		ID:          strings.Join(path, "."),
		Verb:        path[len(path)-1],
		Scopes:      []string{},
		Targets:     append([]string(nil), allTargets...),
		Granted:     true,
		Read:        d.SideEffect == api.SideEffectRead,
		Destructive: d.SideEffect == api.SideEffectDestructive,
		desc:        d,
	}
	if len(path) > 1 {
		c.Noun = strings.Join(path[:len(path)-1], ".")
	} else if opts.ToolName != "" {
		c.Noun = opts.ToolName
		c.ID = opts.ToolName + "." + c.Verb
	} else {
		c.Noun = path[0]
	}
	c.DryRun = c.Destructive

	c.Gate = GateFree
	if c.Destructive {
		c.Gate = GateReview
	}
	if opts.Gate != nil {
		if g := opts.Gate(d); g != "" {
			c.Gate = g
		}
	}

	c.Output = OutputRecord
	if c.DryRun {
		c.Output = OutputPlan
	} else if isArraySchema(d.OutputSchema) {
		c.Output = OutputList
	}

	props := map[string]any{}
	c.props = map[string]string{}
	c.required = map[string]bool{}
	var required []string
	for _, f := range d.Flags {
		if f.Name == "dry-run" {
			continue
		}
		schema, typ := flagSchema(f)
		props[f.Name] = schema
		c.props[f.Name] = typ
		if f.Required {
			required = append(required, f.Name)
			c.required[f.Name] = true
		}
	}
	for _, a := range d.Args {
		if _, clash := props[a.Name]; clash {
			return c, fmt.Errorf("dek: %s: flag and argument share the name %q", c.ID, a.Name)
		}
		props[a.Name] = map[string]any{"type": "string"}
		c.props[a.Name] = "string"
		c.positional = append(c.positional, a.Name)
		if a.Required {
			required = append(required, a.Name)
			c.required[a.Name] = true
		}
	}
	sort.Strings(required)
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	c.ArgsSchema = schema
	return c, nil
}

// flagSchema maps a pflag type to a JSON Schema property and the
// plain JSON type name used to check calls.
func flagSchema(f api.CommandFlag) (map[string]any, string) {
	s := map[string]any{}
	typ := "string"
	switch t := f.Type; {
	case t == "bool":
		typ = "boolean"
	case strings.HasPrefix(t, "int"), strings.HasPrefix(t, "uint"), t == "count":
		typ = "integer"
	case strings.HasPrefix(t, "float"):
		typ = "number"
	case strings.HasSuffix(t, "Slice"), strings.HasSuffix(t, "Array"):
		typ = "array"
		s["items"] = map[string]any{"type": "string"}
	case strings.HasPrefix(t, "stringTo"):
		typ = "object"
	}
	s["type"] = typ
	if f.Description != "" {
		s["description"] = f.Description
	}
	if f.Default != "" {
		switch typ {
		case "boolean":
			if b, err := strconv.ParseBool(f.Default); err == nil {
				s["default"] = b
			}
		case "integer":
			if n, err := strconv.ParseInt(f.Default, 10, 64); err == nil {
				s["default"] = n
			}
		case "number":
			if n, err := strconv.ParseFloat(f.Default, 64); err == nil {
				s["default"] = n
			}
		case "string":
			s["default"] = f.Default
		}
	}
	return s, typ
}

func isArraySchema(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var s struct {
		Type any `json:"type"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	return s.Type == "array"
}

// etagOf is the manifest's strong ETag: a digest of its JSON.
func etagOf(caps []Capability) string {
	raw, _ := json.Marshal(caps)
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}
