package compliance

import "fmt"

// runStaticChecks analyzes a parsed toolspec for 12-factor compliance.
func runStaticChecks(spec *toolspecYAML) []CheckResult {
	results := make([]CheckResult, 0, 12)

	results = append(results, checkSelfDescribing(spec))
	results = append(results, checkStructuredIO(spec))
	results = append(results, checkStreamDisciplineStatic())
	results = append(results, checkContractsErrors(spec))
	results = append(results, checkPreview(spec))
	results = append(results, checkIdempotency(spec))
	results = append(results, checkStateTransparency(spec))
	results = append(results, checkSafeDelegation(spec))
	results = append(results, checkObservableOpsStatic())
	results = append(results, checkProvenanceStatic())
	results = append(results, checkEvolution(spec))
	results = append(results, checkAuthLifecycle(spec))

	return results
}

func pass(f Factor, details string) CheckResult {
	return CheckResult{Factor: f, Name: f.String(), Status: "pass", Details: details}
}

func fail(f Factor, details, suggestion string) CheckResult {
	return CheckResult{Factor: f, Name: f.String(), Status: "fail", Details: details, Suggestion: suggestion}
}

func skip(f Factor, details string) CheckResult {
	return CheckResult{Factor: f, Name: f.String(), Status: "skip", Details: details}
}

// allCommands flattens the command tree.
func allCommands(cmds []commandYAML) []commandYAML {
	var out []commandYAML
	for _, c := range cmds {
		out = append(out, c)
		out = append(out, allCommands(c.Children)...)
	}
	return out
}

// mutatingCommands returns commands with side_effects declared.
func mutatingCommands(cmds []commandYAML) []commandYAML {
	var out []commandYAML
	for _, c := range allCommands(cmds) {
		if c.Contract != nil && len(c.Contract.SideEffects) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// dangerousCommands returns commands with safety.level == "dangerous".
func dangerousCommands(cmds []commandYAML) []commandYAML {
	var out []commandYAML
	for _, c := range allCommands(cmds) {
		if c.Safety != nil && c.Safety.Level == "dangerous" {
			out = append(out, c)
		}
	}
	return out
}

// Factor 1: Self-Describing
func checkSelfDescribing(spec *toolspecYAML) CheckResult {
	f := FactorSelfDescribing
	if len(spec.Commands) == 0 {
		return fail(f, "no commands defined",
			"Add a commands array with at least one named command")
	}
	for _, c := range spec.Commands {
		if c.Name == "" {
			return fail(f, "command missing name",
				"Every command must have a name field")
		}
	}
	return pass(f, "commands array non-empty, all named")
}

// Factor 2: Structured I/O
func checkStructuredIO(spec *toolspecYAML) CheckResult {
	f := FactorStructuredIO
	for _, c := range allCommands(spec.Commands) {
		if c.OutputSchema != nil {
			return pass(f, "output_schema found on "+c.Name)
		}
	}
	return fail(f, "no command has output_schema",
		"Add output_schema to at least one command")
}

// Factor 3: Stream Discipline — runtime only
func checkStreamDisciplineStatic() CheckResult {
	return skip(FactorStreamDiscipline, "runtime check only")
}

// Factor 4: Contracts & Errors
func checkContractsErrors(spec *toolspecYAML) CheckResult {
	f := FactorContractsErrors
	mut := mutatingCommands(spec.Commands)
	if len(mut) == 0 {
		// No mutating commands — check that at least some have contracts
		for _, c := range allCommands(spec.Commands) {
			if c.Contract != nil {
				return pass(f, "contracts found")
			}
		}
		return fail(f, "no contracts declared",
			"Add contract fields (idempotent, side_effects) to commands")
	}
	for _, c := range mut {
		if c.Contract == nil {
			return fail(f, c.Name+" has side_effects but no contract",
				"Add contract fields to mutating commands")
		}
	}
	return pass(f, "all mutating commands have contracts")
}

// Factor 5: Preview
func checkPreview(spec *toolspecYAML) CheckResult {
	f := FactorPreview
	mut := mutatingCommands(spec.Commands)
	if len(mut) == 0 {
		return pass(f, "no mutating commands to preview")
	}
	withPreview := 0
	for _, c := range mut {
		if len(c.PreviewModes) > 0 {
			withPreview++
		}
	}
	if withPreview == 0 {
		return fail(f, "no mutating command has preview_modes",
			"Add preview_modes (e.g. --dry-run) to mutating commands")
	}
	if withPreview < len(mut) {
		return CheckResult{
			Factor:  f,
			Name:    f.String(),
			Status:  "pass",
			Details: fmt.Sprintf("%d/%d mutating commands have preview_modes", withPreview, len(mut)),
		}
	}
	return pass(f, "all mutating commands have preview_modes")
}

// Factor 6: Idempotency
func checkIdempotency(spec *toolspecYAML) CheckResult {
	f := FactorIdempotency
	all := allCommands(spec.Commands)
	if len(all) == 0 {
		return fail(f, "no commands", "Add commands")
	}
	declared := 0
	for _, c := range all {
		if c.Contract != nil && c.Contract.Idempotent != nil {
			declared++
		}
	}
	if declared == 0 {
		return fail(f, "no command declares idempotent",
			"Add contract.idempotent to each command")
	}
	return pass(f, "idempotency declared on commands")
}

// Factor 7: State Transparency
func checkStateTransparency(spec *toolspecYAML) CheckResult {
	f := FactorStateTransparency
	if spec.StateIntrospection == nil ||
		len(spec.StateIntrospection.ConfigCommands) == 0 {
		return fail(f, "no config_commands in state_introspection",
			"Add state_introspection.config_commands")
	}
	return pass(f, "config_commands present")
}

// Factor 8: Safe Delegation
func checkSafeDelegation(spec *toolspecYAML) CheckResult {
	f := FactorSafeDelegation
	dangerous := dangerousCommands(spec.Commands)
	if len(dangerous) == 0 {
		return pass(f, "no dangerous commands")
	}
	for _, c := range dangerous {
		if c.Safety == nil {
			return fail(f, c.Name+" is dangerous but has no safety block",
				"Add safety with requires_confirmation to dangerous commands")
		}
	}
	return pass(f, "all dangerous commands have safety metadata")
}

// Factor 9: Observable Ops — runtime only
func checkObservableOpsStatic() CheckResult {
	return skip(FactorObservableOps, "runtime check only")
}

// Factor 10: Provenance — runtime only
func checkProvenanceStatic() CheckResult {
	return skip(FactorProvenance, "runtime check only")
}

// Factor 11: Evolution
func checkEvolution(spec *toolspecYAML) CheckResult {
	f := FactorEvolution
	if spec.SchemaVersion == "" {
		return fail(f, "schema_version not set",
			"Set schema_version in the toolspec")
	}
	return pass(f, "schema_version: "+spec.SchemaVersion)
}

// Factor 12: Auth Lifecycle
func checkAuthLifecycle(spec *toolspecYAML) CheckResult {
	f := FactorAuthLifecycle
	if spec.StateIntrospection == nil ||
		len(spec.StateIntrospection.AuthCommands) == 0 {
		return skip(f, "no auth_commands — skipped (tool may not need auth)")
	}
	return pass(f, "auth_commands present")
}
