package cmdsurface

import (
	"bytes"
	"fmt"
	"text/template"
	"text/template/parse"
)

// webhookAllowedRoots is the closed set of top-level fields a
// FlagMap or ArgsTemplate may reference. Templates that walk into
// any other root field are rejected at parse time — the spec calls
// for an explicit allow-list because the templates execute against
// adversary-controlled input.
var webhookAllowedRoots = map[string]struct{}{
	"body":    {},
	"headers": {},
	"query":   {},
	"path":    {},
}

// parseWebhookTemplate parses src and verifies every top-level
// field reference is in webhookAllowedRoots. The returned template
// is safe to execute against a {body,headers,query,path} root.
func parseWebhookTemplate(name, src string) (*template.Template, error) {
	t, err := template.New(name).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", name, err)
	}
	if err := checkWebhookTemplateRoots(t); err != nil {
		return nil, fmt.Errorf("template %q: %w", name, err)
	}
	return t, nil
}

// checkWebhookTemplateRoots walks every action node in t and rejects
// templates that reference a top-level field outside the allow-list.
func checkWebhookTemplateRoots(t *template.Template) error {
	if t == nil || t.Root == nil {
		return nil
	}
	return walkWebhookList(t.Root)
}

// walkWebhookList walks a *parse.ListNode and inspects each child.
func walkWebhookList(list *parse.ListNode) error {
	if list == nil {
		return nil
	}
	for _, n := range list.Nodes {
		if err := walkWebhookNode(n); err != nil {
			return err
		}
	}
	return nil
}

// walkWebhookNode dispatches on the parse-tree node type.
func walkWebhookNode(n parse.Node) error {
	switch v := n.(type) {
	case *parse.ActionNode:
		return walkWebhookPipe(v.Pipe)
	case *parse.IfNode:
		if err := walkWebhookPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkWebhookList(v.List); err != nil {
			return err
		}
		return walkWebhookList(v.ElseList)
	case *parse.RangeNode:
		if err := walkWebhookPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkWebhookList(v.List); err != nil {
			return err
		}
		return walkWebhookList(v.ElseList)
	case *parse.WithNode:
		if err := walkWebhookPipe(v.Pipe); err != nil {
			return err
		}
		if err := walkWebhookList(v.List); err != nil {
			return err
		}
		return walkWebhookList(v.ElseList)
	case *parse.TemplateNode:
		return walkWebhookPipe(v.Pipe)
	}
	return nil
}

// walkWebhookPipe inspects every command in a pipe.
func walkWebhookPipe(p *parse.PipeNode) error {
	if p == nil {
		return nil
	}
	for _, c := range p.Cmds {
		for _, arg := range c.Args {
			if err := walkWebhookArg(arg); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkWebhookArg checks one command argument. FieldNodes at the top
// level (i.e. starting with ".") name the root field — that is the
// gate. PipeNode arguments recurse.
func walkWebhookArg(arg parse.Node) error {
	switch v := arg.(type) {
	case *parse.FieldNode:
		if len(v.Ident) == 0 {
			return nil
		}
		if _, ok := webhookAllowedRoots[v.Ident[0]]; !ok {
			return fmt.Errorf("disallowed template root .%s (allowed: body, headers, query, path)", v.Ident[0])
		}
	case *parse.PipeNode:
		return walkWebhookPipe(v)
	}
	return nil
}

// execWebhookTemplate executes t against root and returns the
// rendered string. text/template emits "<no value>" for missing
// fields under the default missingkey mode; we keep that behavior
// to match the spec ("documented behavior; the test pins it").
func execWebhookTemplate(t *template.Template, root map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, root); err != nil {
		return "", err
	}
	return buf.String(), nil
}
