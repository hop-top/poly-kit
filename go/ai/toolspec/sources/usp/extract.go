// Package usp learns tool workflows from real agent sessions by
// analyzing Bash/shell tool calls (User Session Patterns).
package usp

import "strings"

// ToolCall represents a tool invocation extracted from a session.
type ToolCall struct {
	Name  string // "Bash", "shell", "Read", etc.
	Input string // the command string or file path
}

// ParsedCommand is a parsed CLI invocation.
type ParsedCommand struct {
	Tool   string   // e.g. "git", "docker", "gh"
	SubCmd string   // e.g. "commit", "build", "pr"
	Args   []string // remaining args
	Raw    string   // original command string
}

// Extract filters tool calls to Bash/shell, parses commands.
func Extract(calls []ToolCall) []ParsedCommand {
	var out []ParsedCommand
	for _, c := range calls {
		if !isBash(c.Name) {
			continue
		}
		input := strings.TrimSpace(c.Input)
		if input == "" {
			continue
		}
		// Take first command before pipe.
		if idx := findPipe(input); idx >= 0 {
			input = strings.TrimSpace(input[:idx])
			if input == "" {
				continue
			}
		}
		tokens := tokenize(input)
		if len(tokens) == 0 {
			continue
		}
		pc := ParsedCommand{
			Tool: tokens[0],
			Raw:  c.Input,
		}
		if len(tokens) > 1 && !isFlag(tokens[1]) {
			pc.SubCmd = tokens[1]
			pc.Args = tokens[2:]
		} else if len(tokens) > 1 {
			pc.Args = tokens[1:]
		}
		out = append(out, pc)
	}
	return out
}

func isBash(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell":
		return true
	}
	return false
}

// isFlag returns true when the token starts with "-".
func isFlag(s string) bool {
	return len(s) > 0 && s[0] == '-'
}

// findPipe returns the index of the first unquoted pipe character,
// or -1 if none found.
func findPipe(s string) int {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '|':
			if !inSingle && !inDouble {
				return i
			}
		}
	}
	return -1
}

// tokenize splits a command string on whitespace, respecting single
// and double quotes. Quotes are stripped from the returned tokens.
func tokenize(s string) []string {
	var tokens []string
	var buf strings.Builder
	var inSingle, inDouble bool

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == '\\' && inDouble && i+1 < len(s):
			i++
			buf.WriteByte(s[i])
		case (ch == ' ' || ch == '\t') && !inSingle && !inDouble:
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteByte(ch)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}
