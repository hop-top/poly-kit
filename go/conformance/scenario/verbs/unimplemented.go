package verbs

// auth_lifecycle_clean: parsed-but-deferred per Q2. The validator
// accepts the verb (it's in contracts/scenario-rules.json) but the
// grader returns StatusNotImplemented for every invocation.
//
// Adopters can already declare this verb in their scenarios; once
// the auth-lifecycle harness lands the Evaluate function gets wired
// here in a follow-up track. The rules JSON entry carries
// implementation_status: deferred for adopter-side documentation.

func init() {
	register(&Entry{
		Kind:     KindAuthLifecycleClean,
		Validate: nil,
		Evaluate: nil, // nil signals not-implemented to the grader
	})
}
