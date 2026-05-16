package scenario

import "hop.top/kit/go/console/output"

// AsCLIError lets *GraderError satisfy the kit middleware's
// conversion interface so RunE in the grade CLI leaf can return it
// directly and round-trip the envelope through fang.Execute.
func (e *GraderError) AsCLIError() *output.Error {
	if e == nil {
		return nil
	}
	return &output.Error{
		Code:     e.Code,
		Message:  e.Message,
		ExitCode: e.ExitCode,
	}
}

// AsCLIError converts a *ParseError to the kit error envelope.
// Parse errors map to CodeScenarioParseError (exit 2).
func (e *ParseError) AsCLIError() *output.Error {
	if e == nil {
		return nil
	}
	return &output.Error{
		Code:     output.CodeScenarioParseError,
		Message:  e.Error(),
		ExitCode: 2,
	}
}

// AsCLIError converts a *SchemaUnsupportedError to the kit error
// envelope. Maps to CodeScenarioSchemaUnsupported (exit 1).
func (e *SchemaUnsupportedError) AsCLIError() *output.Error {
	if e == nil {
		return nil
	}
	return &output.Error{
		Code:     output.CodeScenarioSchemaUnsupported,
		Message:  e.Error(),
		ExitCode: 1,
	}
}

// AsCLIError converts ValidationErrors to the kit error envelope.
// Validation errors map to CodeScenarioValidateError (exit 2).
func (e *ValidationErrors) AsCLIError() *output.Error {
	if e == nil {
		return nil
	}
	return &output.Error{
		Code:     output.CodeScenarioValidateError,
		Message:  e.Error(),
		ExitCode: 2,
	}
}
