package toolspec

// Merge returns a new ToolSpec where overlay fills gaps in base.
// Name is taken from overlay only if base.Name is empty.
// Slice fields (Commands, Flags, ErrorPatterns, Workflows) are
// taken from overlay only if the base slice is empty.
func Merge(base, overlay *ToolSpec) *ToolSpec {
	out := ToolSpec{
		Name:               base.Name,
		SchemaVersion:      base.SchemaVersion,
		Commands:           copyCommands(base.Commands),
		Flags:              copyFlags(base.Flags),
		ErrorPatterns:      copyErrors(base.ErrorPatterns),
		Workflows:          copyWorkflows(base.Workflows),
		StateIntrospection: copyStateIntrospection(base.StateIntrospection),
	}
	if out.Name == "" {
		out.Name = overlay.Name
	}
	if out.SchemaVersion == "" {
		out.SchemaVersion = overlay.SchemaVersion
	}
	if len(out.Commands) == 0 {
		out.Commands = copyCommands(overlay.Commands)
	}
	if len(out.Flags) == 0 {
		out.Flags = copyFlags(overlay.Flags)
	}
	if len(out.ErrorPatterns) == 0 {
		out.ErrorPatterns = copyErrors(overlay.ErrorPatterns)
	}
	if len(out.Workflows) == 0 {
		out.Workflows = copyWorkflows(overlay.Workflows)
	}
	if out.StateIntrospection == nil {
		out.StateIntrospection = copyStateIntrospection(overlay.StateIntrospection)
	}
	return &out
}

// Diff returns a ToolSpec containing fields present in b but not a.
// Name is included if b has a name and a does not.
// Slice fields are included if b has entries and a has none.
func Diff(a, b *ToolSpec) *ToolSpec {
	out := ToolSpec{}
	if a.Name == "" && b.Name != "" {
		out.Name = b.Name
	}
	if a.SchemaVersion == "" && b.SchemaVersion != "" {
		out.SchemaVersion = b.SchemaVersion
	}
	if len(a.Commands) == 0 && len(b.Commands) > 0 {
		out.Commands = copyCommands(b.Commands)
	}
	if len(a.Flags) == 0 && len(b.Flags) > 0 {
		out.Flags = copyFlags(b.Flags)
	}
	if len(a.ErrorPatterns) == 0 && len(b.ErrorPatterns) > 0 {
		out.ErrorPatterns = copyErrors(b.ErrorPatterns)
	}
	if len(a.Workflows) == 0 && len(b.Workflows) > 0 {
		out.Workflows = copyWorkflows(b.Workflows)
	}
	if a.StateIntrospection == nil && b.StateIntrospection != nil {
		out.StateIntrospection = copyStateIntrospection(b.StateIntrospection)
	}
	return &out
}

func copyCommands(s []Command) []Command {
	if s == nil {
		return nil
	}
	out := make([]Command, len(s))
	copy(out, s)
	return out
}

func copyFlags(s []Flag) []Flag {
	if s == nil {
		return nil
	}
	out := make([]Flag, len(s))
	copy(out, s)
	return out
}

func copyErrors(s []ErrorPattern) []ErrorPattern {
	if s == nil {
		return nil
	}
	out := make([]ErrorPattern, len(s))
	copy(out, s)
	return out
}

func copyWorkflows(s []Workflow) []Workflow {
	if s == nil {
		return nil
	}
	out := make([]Workflow, len(s))
	copy(out, s)
	return out
}

func copyStateIntrospection(si *StateIntrospection) *StateIntrospection {
	if si == nil {
		return nil
	}
	out := &StateIntrospection{}
	if si.ConfigCommands != nil {
		out.ConfigCommands = make([]string, len(si.ConfigCommands))
		copy(out.ConfigCommands, si.ConfigCommands)
	}
	if si.EnvVars != nil {
		out.EnvVars = make([]string, len(si.EnvVars))
		copy(out.EnvVars, si.EnvVars)
	}
	if si.AuthCommands != nil {
		out.AuthCommands = make([]string, len(si.AuthCommands))
		copy(out.AuthCommands, si.AuthCommands)
	}
	return out
}
